package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Service implements all business logic for the messaging domain.
type Service struct {
	db  *database.DB
	now func() time.Time
}

// NewService creates a new messaging Service.
func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// ------------------------------------------------------------------ helpers

func (s *Service) activeUserOrThrow(ctx context.Context, userID string) error {
	var isActive bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "isActive" FROM "User" WHERE "id" = $1`, userID,
	).Scan(&isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !isActive {
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
	}
	return nil
}

func (s *Service) blockedEitherWay(ctx context.Context, a, b string) (bool, error) {
	var found bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM "Block"
			WHERE ("blockerId" = $1 AND "blockedId" = $2)
			   OR ("blockerId" = $2 AND "blockedId" = $1)
		)`, a, b,
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check block: %w", err)
	}
	return found, nil
}

// blockedEitherWayTx checks for a block within a transaction.
func blockedEitherWayTx(ctx context.Context, tx pgx.Tx, a, b string) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM "Block"
			WHERE ("blockerId" = $1 AND "blockedId" = $2)
			   OR ("blockerId" = $2 AND "blockedId" = $1)
		)`, a, b,
	).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("check block: %w", err)
	}
	return found, nil
}

// membershipOrThrow verifies that viewerID is a member of conversationID,
// returning a 404 if not found (indistinguishable from non-existence).
func (s *Service) membershipOrThrow(ctx context.Context, viewerID, conversationID string) error {
	var exists bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM "ConversationMember"
			WHERE "conversationId" = $1 AND "userId" = $2
		)`, conversationID, viewerID,
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("check membership: %w", err)
	}
	if !exists {
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Conversation not found.")
	}
	return nil
}

// scanMessageRow scans a message row from any row scanner.
func scanMessageRow(row interface{ Scan(...any) error }) (messageRow, error) {
	var m messageRow
	err := row.Scan(
		&m.ID, &m.ConversationID, &m.SenderID, &m.ClientMessageID,
		&m.Body, &m.DeletedAt, &m.CreatedAt, &m.UpdatedAt,
	)
	return m, err
}

// projectConversation builds a full ConversationView for viewerID + conversationID.
func (s *Service) projectConversation(ctx context.Context, viewerID, conversationID string) (ConversationView, error) {
	// Load membership (unreadCount) + conversation basics in one query.
	type convRow struct {
		ConvID        string
		ConvType      string
		LastMessageAt *time.Time
		LastMessageID *string
		ConvCreatedAt time.Time
		UnreadCount   int
	}
	var cr convRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT c."id", c."type", c."lastMessageAt", c."lastMessageId", c."createdAt",
		       cm."unreadCount"
		FROM "Conversation" c
		JOIN "ConversationMember" cm
		  ON cm."conversationId" = c."id" AND cm."userId" = $2
		WHERE c."id" = $1`, conversationID, viewerID,
	).Scan(&cr.ConvID, &cr.ConvType, &cr.LastMessageAt, &cr.LastMessageID, &cr.ConvCreatedAt, &cr.UnreadCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationView{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Conversation not found.")
		}
		return ConversationView{}, fmt.Errorf("load conversation: %w", err)
	}

	// Load peer.
	var peer ConversationPeerView
	err = s.db.Pool.QueryRow(ctx, `
		SELECT u."id", u."displayName", u."avatarUrl"
		FROM "ConversationMember" cm
		JOIN "User" u ON u."id" = cm."userId"
		WHERE cm."conversationId" = $1 AND cm."userId" != $2
		LIMIT 1`, conversationID, viewerID,
	).Scan(&peer.ID, &peer.DisplayName, &peer.AvatarURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ConversationView{}, httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "Conversation is no longer available.")
		}
		return ConversationView{}, fmt.Errorf("load peer: %w", err)
	}

	// Optionally load last message.
	var lastMsg *LastMessageView
	if cr.LastMessageID != nil {
		var m messageRow
		row := s.db.Pool.QueryRow(ctx, `
			SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
			FROM "Message"
			WHERE "id" = $1`, *cr.LastMessageID)
		m, err = scanMessageRow(row)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ConversationView{}, fmt.Errorf("load last message: %w", err)
		}
		if err == nil {
			lmv := toLastMessageView(m)
			lastMsg = &lmv
		}
	}

	return ConversationView{
		ID:            cr.ConvID,
		Type:          cr.ConvType,
		Participant:   peer,
		LastMessage:   lastMsg,
		LastMessageAt: cr.LastMessageAt,
		UnreadCount:   cr.UnreadCount,
		CreatedAt:     cr.ConvCreatedAt,
	}, nil
}

// ------------------------------------------------------------------ GetOrCreateDirect

// GetOrCreateDirect gets or creates a direct conversation between viewer and target.
func (s *Service) GetOrCreateDirect(ctx context.Context, viewerID, targetID string) (ConversationView, bool, error) {
	if viewerID == targetID {
		return ConversationView{}, false, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "You cannot message yourself.")
	}
	if err := s.activeUserOrThrow(ctx, targetID); err != nil {
		return ConversationView{}, false, err
	}
	blocked, err := s.blockedEitherWay(ctx, viewerID, targetID)
	if err != nil {
		return ConversationView{}, false, err
	}
	if blocked {
		return ConversationView{}, false, httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "You cannot message this user.")
	}

	key := directKey(viewerID, targetID)

	// Check for existing conversation.
	var existingID string
	err = s.db.Pool.QueryRow(ctx,
		`SELECT "id" FROM "Conversation" WHERE "directKey" = $1`, key,
	).Scan(&existingID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ConversationView{}, false, fmt.Errorf("lookup conversation: %w", err)
	}
	if err == nil {
		view, err := s.projectConversation(ctx, viewerID, existingID)
		return view, false, err
	}

	// Create new conversation in a transaction.
	var createdID string
	txErr := s.db.InTx(ctx, func(tx pgx.Tx) error {
		// Re-check block inside transaction.
		blocked, err := blockedEitherWayTx(ctx, tx, viewerID, targetID)
		if err != nil {
			return err
		}
		if blocked {
			return httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "You cannot message this user.")
		}

		newID := uuid.New().String()
		now := s.now()
		_, err = tx.Exec(ctx, `
			INSERT INTO "Conversation" ("id", "type", "directKey", "createdAt", "updatedAt")
			VALUES ($1, 'DIRECT', $2, $3, $3)`,
			newID, key, now,
		)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO "ConversationMember" ("id", "conversationId", "userId", "joinedAt")
			VALUES ($1, $3, $4, $5), ($2, $3, $6, $5)`,
			uuid.New().String(), uuid.New().String(), newID, viewerID, now, targetID,
		)
		if err != nil {
			return err
		}
		createdID = newID
		return nil
	})
	if txErr != nil {
		if database.IsUniqueViolation(txErr) {
			// Lost a concurrent creation race: return the winner idempotently.
			err = s.db.Pool.QueryRow(ctx,
				`SELECT "id" FROM "Conversation" WHERE "directKey" = $1`, key,
			).Scan(&existingID)
			if err != nil {
				return ConversationView{}, false, fmt.Errorf("lookup winner: %w", err)
			}
			view, err := s.projectConversation(ctx, viewerID, existingID)
			return view, false, err
		}
		return ConversationView{}, false, txErr
	}

	view, err := s.projectConversation(ctx, viewerID, createdID)
	return view, true, err
}

// ------------------------------------------------------------------ List

// convListRow is the internal shape loaded for conversation list.
type convListRow struct {
	ConvID        string
	ConvType      string
	LastMessageAt *time.Time
	LastMessageID *string
	ConvCreatedAt time.Time
	UnreadCount   int
}

// List returns a cursor-paginated list of conversations for viewerID.
func (s *Service) List(ctx context.Context, viewerID string, page httpx.Page) (httpx.PageResult[ConversationView], error) {
	limit := page.Limit

	var (
		rows []convListRow
		err  error
	)

	if page.Cursor == nil {
		rows, err = s.listConversations(ctx, viewerID, nil, limit+1)
	} else {
		rows, err = s.listConversations(ctx, viewerID, page.Cursor, limit+1)
	}
	if err != nil {
		return httpx.PageResult[ConversationView]{}, err
	}

	pageRows := rows
	hasMore := len(rows) > limit
	if hasMore {
		pageRows = rows[:limit]
	}

	if len(pageRows) == 0 {
		return httpx.PageResult[ConversationView]{Items: []ConversationView{}}, nil
	}

	// Batch-load peers and last messages.
	ids := make([]string, len(pageRows))
	for i, r := range pageRows {
		ids[i] = r.ConvID
	}

	peerByConv, err := s.batchLoadPeers(ctx, ids, viewerID)
	if err != nil {
		return httpx.PageResult[ConversationView]{}, err
	}

	msgIDs := make([]string, 0, len(pageRows))
	for _, r := range pageRows {
		if r.LastMessageID != nil {
			msgIDs = append(msgIDs, *r.LastMessageID)
		}
	}
	msgByID, err := s.batchLoadMessages(ctx, msgIDs)
	if err != nil {
		return httpx.PageResult[ConversationView]{}, err
	}

	items := make([]ConversationView, 0, len(pageRows))
	for _, r := range pageRows {
		peer, ok := peerByConv[r.ConvID]
		if !ok {
			return httpx.PageResult[ConversationView]{}, httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "Conversation is no longer available.")
		}
		var lastMsg *LastMessageView
		if r.LastMessageID != nil {
			if m, ok := msgByID[*r.LastMessageID]; ok {
				lmv := toLastMessageView(m)
				lastMsg = &lmv
			}
		}
		items = append(items, ConversationView{
			ID:            r.ConvID,
			Type:          r.ConvType,
			Participant:   peer,
			LastMessage:   lastMsg,
			LastMessageAt: r.LastMessageAt,
			UnreadCount:   r.UnreadCount,
			CreatedAt:     r.ConvCreatedAt,
		})
	}

	result := httpx.PageResult[ConversationView]{Items: items}
	if hasMore {
		last := pageRows[limit-1]
		cursorTime := time.Time{} // epoch 0 for NULL lastMessageAt
		if last.LastMessageAt != nil {
			cursorTime = *last.LastMessageAt
		}
		cursor := httpx.EncodeCursor(cursorTime, last.ConvID)
		result.NextCursor = &cursor
	}
	return result, nil
}

func (s *Service) listConversations(ctx context.Context, viewerID string, cursor *httpx.Cursor, limit int) ([]convListRow, error) {
	var query string
	var args []any

	if cursor == nil {
		query = `
			SELECT c."id", c."type", c."lastMessageAt", c."lastMessageId", c."createdAt", cm."unreadCount"
			FROM "ConversationMember" cm
			JOIN "Conversation" c ON c."id" = cm."conversationId"
			WHERE cm."userId" = $1
			ORDER BY c."lastMessageAt" DESC NULLS LAST, c."id" DESC
			LIMIT $2`
		args = []any{viewerID, limit}
	} else if cursor.CreatedAt.IsZero() {
		// Past all conversations with a lastMessageAt, now in NULL territory.
		query = `
			SELECT c."id", c."type", c."lastMessageAt", c."lastMessageId", c."createdAt", cm."unreadCount"
			FROM "ConversationMember" cm
			JOIN "Conversation" c ON c."id" = cm."conversationId"
			WHERE cm."userId" = $1
			  AND c."lastMessageAt" IS NULL
			  AND c."id" < $2
			ORDER BY c."lastMessageAt" DESC NULLS LAST, c."id" DESC
			LIMIT $3`
		args = []any{viewerID, cursor.ID, limit}
	} else {
		// Standard keyset: lastMessageAt < cursor OR same ts with smaller id OR NULL.
		query = `
			SELECT c."id", c."type", c."lastMessageAt", c."lastMessageId", c."createdAt", cm."unreadCount"
			FROM "ConversationMember" cm
			JOIN "Conversation" c ON c."id" = cm."conversationId"
			WHERE cm."userId" = $1
			  AND (
			        c."lastMessageAt" < $2
			     OR (c."lastMessageAt" = $2 AND c."id" < $3)
			     OR c."lastMessageAt" IS NULL
			  )
			ORDER BY c."lastMessageAt" DESC NULLS LAST, c."id" DESC
			LIMIT $4`
		args = []any{viewerID, cursor.CreatedAt, cursor.ID, limit}
	}

	pgRows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list conversations: %w", err)
	}
	defer pgRows.Close()

	var out []convListRow
	for pgRows.Next() {
		var r convListRow
		if err := pgRows.Scan(&r.ConvID, &r.ConvType, &r.LastMessageAt, &r.LastMessageID, &r.ConvCreatedAt, &r.UnreadCount); err != nil {
			return nil, fmt.Errorf("scan conversation row: %w", err)
		}
		out = append(out, r)
	}
	return out, pgRows.Err()
}

func (s *Service) batchLoadPeers(ctx context.Context, conversationIDs []string, viewerID string) (map[string]ConversationPeerView, error) {
	if len(conversationIDs) == 0 {
		return map[string]ConversationPeerView{}, nil
	}
	// Build parameterized IN clause.
	args := make([]any, 0, len(conversationIDs)+1)
	args = append(args, viewerID)
	in := ""
	for i, id := range conversationIDs {
		if i > 0 {
			in += ","
		}
		args = append(args, id)
		in += fmt.Sprintf("$%d", i+2)
	}
	query := fmt.Sprintf(`
		SELECT cm."conversationId", u."id", u."displayName", u."avatarUrl"
		FROM "ConversationMember" cm
		JOIN "User" u ON u."id" = cm."userId"
		WHERE cm."userId" != $1
		  AND cm."conversationId" IN (%s)`, in)

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch load peers: %w", err)
	}
	defer rows.Close()

	result := make(map[string]ConversationPeerView, len(conversationIDs))
	for rows.Next() {
		var convID string
		var peer ConversationPeerView
		if err := rows.Scan(&convID, &peer.ID, &peer.DisplayName, &peer.AvatarURL); err != nil {
			return nil, fmt.Errorf("scan peer: %w", err)
		}
		result[convID] = peer
	}
	return result, rows.Err()
}

func (s *Service) batchLoadMessages(ctx context.Context, messageIDs []string) (map[string]messageRow, error) {
	if len(messageIDs) == 0 {
		return map[string]messageRow{}, nil
	}
	args := make([]any, len(messageIDs))
	in := ""
	for i, id := range messageIDs {
		if i > 0 {
			in += ","
		}
		args[i] = id
		in += fmt.Sprintf("$%d", i+1)
	}
	query := fmt.Sprintf(`
		SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
		FROM "Message"
		WHERE "id" IN (%s)`, in)

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch load messages: %w", err)
	}
	defer rows.Close()

	result := make(map[string]messageRow, len(messageIDs))
	for rows.Next() {
		m, err := scanMessageRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		result[m.ID] = m
	}
	return result, rows.Err()
}

// ------------------------------------------------------------------ UnreadTotal

// UnreadTotal returns the sum of unread counts across all of viewerID's conversations.
func (s *Service) UnreadTotal(ctx context.Context, viewerID string) (int, error) {
	var total int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COALESCE(SUM("unreadCount"), 0)
		FROM "ConversationMember"
		WHERE "userId" = $1`, viewerID,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("unread total: %w", err)
	}
	return total, nil
}

// ------------------------------------------------------------------ Detail

// Detail returns a ConversationView for a single conversation.
func (s *Service) Detail(ctx context.Context, viewerID, conversationID string) (ConversationView, error) {
	return s.projectConversation(ctx, viewerID, conversationID)
}

// ------------------------------------------------------------------ SendMessage

// SendMessage sends a message idempotently. Returns (view, created, error).
func (s *Service) SendMessage(ctx context.Context, viewerID, conversationID, clientMessageID, body string) (MessageView, bool, error) {
	if err := s.membershipOrThrow(ctx, viewerID, conversationID); err != nil {
		return MessageView{}, false, err
	}

	// Idempotent check: same sender + clientMessageId → return original.
	var existing messageRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
		FROM "Message"
		WHERE "senderId" = $1 AND "clientMessageId" = $2`,
		viewerID, clientMessageID,
	).Scan(
		&existing.ID, &existing.ConversationID, &existing.SenderID, &existing.ClientMessageID,
		&existing.Body, &existing.DeletedAt, &existing.CreatedAt, &existing.UpdatedAt,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return MessageView{}, false, fmt.Errorf("check idempotency: %w", err)
	}
	if err == nil {
		return toMessageView(existing), false, nil
	}

	// Create new message in a transaction.
	var created messageRow
	txErr := s.db.InTx(ctx, func(tx pgx.Tx) error {
		// Find peer within transaction.
		var peerID string
		err := tx.QueryRow(ctx, `
			SELECT "userId" FROM "ConversationMember"
			WHERE "conversationId" = $1 AND "userId" != $2
			LIMIT 1`, conversationID, viewerID,
		).Scan(&peerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "Conversation is no longer available.")
			}
			return fmt.Errorf("find peer: %w", err)
		}

		// Re-check block inside transaction.
		blocked, err := blockedEitherWayTx(ctx, tx, viewerID, peerID)
		if err != nil {
			return err
		}
		if blocked {
			return httpx.NewError(http.StatusForbidden, httpx.CodeForbidden, "You cannot message this user.")
		}

		msgID := uuid.New().String()
		now := s.now()

		err = tx.QueryRow(ctx, `
			INSERT INTO "Message" ("id","conversationId","senderId","clientMessageId","body","createdAt","updatedAt")
			VALUES ($1,$2,$3,$4,$5,$6,$6)
			RETURNING "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"`,
			msgID, conversationID, viewerID, clientMessageID, body, now,
		).Scan(
			&created.ID, &created.ConversationID, &created.SenderID, &created.ClientMessageID,
			&created.Body, &created.DeletedAt, &created.CreatedAt, &created.UpdatedAt,
		)
		if err != nil {
			return fmt.Errorf("insert message: %w", err)
		}

		// Write DomainEvent outbox entry.
		payload, _ := json.Marshal(map[string]string{
			"messageId":      msgID,
			"conversationId": conversationID,
		})
		_, err = tx.Exec(ctx, `
			INSERT INTO "DomainEvent" ("id","type","aggregateType","aggregateId","actorId","payload","status","attempts","availableAt","createdAt","processedAt")
			VALUES (gen_random_uuid()::text, 'message.created', 'Message', $1, $2, $3::jsonb, 'PENDING', 0, NOW(), NOW(), NULL)`,
			msgID, viewerID, string(payload),
		)
		if err != nil {
			return fmt.Errorf("insert domain event: %w", err)
		}

		// Update conversation's lastMessageAt/lastMessageId.
		_, err = tx.Exec(ctx, `
			UPDATE "Conversation"
			SET "lastMessageAt" = $2, "lastMessageId" = $3, "updatedAt" = $2
			WHERE "id" = $1`,
			conversationID, now, msgID,
		)
		if err != nil {
			return fmt.Errorf("update conversation: %w", err)
		}

		// Increment peer's unreadCount.
		_, err = tx.Exec(ctx, `
			UPDATE "ConversationMember"
			SET "unreadCount" = "unreadCount" + 1
			WHERE "conversationId" = $1 AND "userId" = $2`,
			conversationID, peerID,
		)
		if err != nil {
			return fmt.Errorf("increment unread: %w", err)
		}

		return nil
	})
	if txErr != nil {
		if database.IsUniqueViolation(txErr) {
			// Lost a concurrent duplicate-send race: return the winner.
			row := s.db.Pool.QueryRow(ctx, `
				SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
				FROM "Message"
				WHERE "senderId" = $1 AND "clientMessageId" = $2`,
				viewerID, clientMessageID,
			)
			winner, err := scanMessageRow(row)
			if err != nil {
				return MessageView{}, false, fmt.Errorf("load winner message: %w", err)
			}
			return toMessageView(winner), false, nil
		}
		return MessageView{}, false, txErr
	}

	return toMessageView(created), true, nil
}

// ------------------------------------------------------------------ ListMessages

// ListMessages returns a cursor-paginated list of messages for a conversation.
func (s *Service) ListMessages(ctx context.Context, viewerID, conversationID string, page httpx.Page) (httpx.PageResult[MessageView], error) {
	if err := s.membershipOrThrow(ctx, viewerID, conversationID); err != nil {
		return httpx.PageResult[MessageView]{}, err
	}

	var query string
	var args []any

	if page.Cursor == nil {
		query = `
			SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
			FROM "Message"
			WHERE "conversationId" = $1
			ORDER BY "createdAt" DESC, "id" DESC
			LIMIT $2`
		args = []any{conversationID, page.Limit + 1}
	} else {
		query = `
			SELECT "id","conversationId","senderId","clientMessageId","body","deletedAt","createdAt","updatedAt"
			FROM "Message"
			WHERE "conversationId" = $1
			  AND ("createdAt" < $2 OR ("createdAt" = $2 AND "id" < $3))
			ORDER BY "createdAt" DESC, "id" DESC
			LIMIT $4`
		args = []any{conversationID, page.Cursor.CreatedAt, page.Cursor.ID, page.Limit + 1}
	}

	pgRows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[MessageView]{}, fmt.Errorf("list messages: %w", err)
	}
	defer pgRows.Close()

	var msgRows []messageRow
	for pgRows.Next() {
		m, err := scanMessageRow(pgRows)
		if err != nil {
			return httpx.PageResult[MessageView]{}, fmt.Errorf("scan message: %w", err)
		}
		msgRows = append(msgRows, m)
	}
	if err := pgRows.Err(); err != nil {
		return httpx.PageResult[MessageView]{}, fmt.Errorf("iterate messages: %w", err)
	}

	hasMore := len(msgRows) > page.Limit
	pageRows := msgRows
	if hasMore {
		pageRows = msgRows[:page.Limit]
	}

	items := make([]MessageView, len(pageRows))
	for i, m := range pageRows {
		items[i] = toMessageView(m)
	}

	result := httpx.PageResult[MessageView]{Items: items}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		cursor := httpx.EncodeCursor(last.CreatedAt, last.ID)
		result.NextCursor = &cursor
	}
	return result, nil
}

// ------------------------------------------------------------------ MarkRead

// MarkReadResult is returned by MarkRead.
type MarkReadResult struct {
	ConversationID string    `json:"conversationId"`
	Unread         int       `json:"unread"`
	LastReadAt     time.Time `json:"lastReadAt"`
}

// MarkRead marks a conversation as read for the viewer.
func (s *Service) MarkRead(ctx context.Context, viewerID, conversationID string) (MarkReadResult, error) {
	if err := s.membershipOrThrow(ctx, viewerID, conversationID); err != nil {
		return MarkReadResult{}, err
	}

	now := s.now()
	_, err := s.db.Pool.Exec(ctx, `
		UPDATE "ConversationMember"
		SET "lastReadAt" = $3, "unreadCount" = 0
		WHERE "conversationId" = $1 AND "userId" = $2
		  AND ("lastReadAt" IS NULL OR "lastReadAt" < $3)`,
		conversationID, viewerID, now,
	)
	if err != nil {
		return MarkReadResult{}, fmt.Errorf("mark read: %w", err)
	}

	// Read back actual values (another concurrent writer could have moved the cursor forward).
	var unread int
	var lastReadAt *time.Time
	err = s.db.Pool.QueryRow(ctx, `
		SELECT "unreadCount", "lastReadAt"
		FROM "ConversationMember"
		WHERE "conversationId" = $1 AND "userId" = $2`,
		conversationID, viewerID,
	).Scan(&unread, &lastReadAt)
	if err != nil {
		return MarkReadResult{}, fmt.Errorf("read back member: %w", err)
	}

	readAt := now
	if lastReadAt != nil {
		readAt = *lastReadAt
	}

	return MarkReadResult{
		ConversationID: conversationID,
		Unread:         unread,
		LastReadAt:     readAt,
	}, nil
}

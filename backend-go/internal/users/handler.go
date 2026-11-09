package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	db  *database.DB
	now func() time.Time
}

func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

func (s *Service) Me(ctx context.Context, userID string) (SafeUser, error) {
	user, err := ScanUser(s.db.Pool.QueryRow(ctx, `SELECT `+UserColumns+` FROM "User" WHERE "id" = $1`, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SafeUser{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
		}
		return SafeUser{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return SafeUser{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
	}
	return ToSafeUser(user), nil
}

// UpdateValue is one property of a profile update. The legacy DTO copies every
// payload key, so an explicit null is distinct from an absent property: it
// clears the column, while an absent property leaves it alone.
type UpdateValue struct {
	Present bool
	Null    bool
	Text    string
}

func (v UpdateValue) pointer() *string {
	if v.Null {
		return nil
	}
	text := v.Text
	return &text
}

type ProfileUpdate struct {
	DisplayName UpdateValue
	Bio         UpdateValue
	AvatarURL   UpdateValue
}

func (u ProfileUpdate) empty() bool {
	return !u.DisplayName.Present && !u.Bio.Present && !u.AvatarURL.Present
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, update ProfileUpdate) (SafeUser, error) {
	if update.empty() {
		return s.Me(ctx, userID)
	}
	if update.DisplayName.Present && update.DisplayName.Null {
		return SafeUser{}, httpx.Internal(errors.New("displayName may not be null"))
	}
	sets := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if update.DisplayName.Present {
		args = append(args, nullableTrim(update.DisplayName.Text))
		sets = append(sets, fmt.Sprintf(`"displayName" = $%d`, len(args)))
	}
	if update.Bio.Present {
		args = append(args, update.Bio.pointer())
		sets = append(sets, fmt.Sprintf(`"bio" = $%d`, len(args)))
	}
	if update.AvatarURL.Present {
		args = append(args, update.AvatarURL.pointer())
		sets = append(sets, fmt.Sprintf(`"avatarUrl" = $%d`, len(args)))
	}
	args = append(args, s.now())
	sets = append(sets, fmt.Sprintf(`"updatedAt" = $%d`, len(args)))
	args = append(args, userID)

	row := s.db.Pool.QueryRow(ctx,
		`UPDATE "User" SET `+joinComma(sets)+` WHERE "id" = $`+fmt.Sprint(len(args))+` RETURNING `+UserColumns,
		args...)
	user, err := ScanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SafeUser{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
		}
		return SafeUser{}, fmt.Errorf("update profile: %w", err)
	}
	return ToSafeUser(user), nil
}

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(users chi.Router) {
	users.Get("/me", h.me)
	users.Patch("/me", h.updateMe)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.service.Me(r.Context(), identity.PrincipalFromRequest(r).Subject)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user)
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	update, err := DecodeProfileUpdate(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	user, err := h.service.UpdateProfile(r.Context(), identity.PrincipalFromRequest(r).Subject, update)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user)
}

func DecodeProfileUpdate(r *http.Request) (ProfileUpdate, error) {
	body, err := httpx.DecodeBody(r, "displayName", "bio", "avatarUrl")
	if err != nil {
		return ProfileUpdate{}, err
	}
	displayName := body.String("displayName").Transform(validate.Trim).Optional()
	bio := body.String("bio").Optional()
	avatarURL := body.String("avatarUrl").Optional()

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(displayName, 120)
	errors.IsString(displayName)
	errors.MaxLength(bio, 500)
	errors.IsString(bio)
	errors.MaxLength(avatarURL, 2048)
	errors.URL(avatarURL)
	if len(errors) > 0 {
		return ProfileUpdate{}, httpx.ValidationError(errors)
	}

	return ProfileUpdate{
		DisplayName: updateValue(displayName),
		Bio:         updateValue(bio),
		AvatarURL:   updateValue(avatarURL),
	}, nil
}

func updateValue(field validate.Field) UpdateValue {
	return UpdateValue{Present: field.Present(), Null: field.IsNull, Text: field.Value()}
}

func nullableTrim(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func joinComma(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}

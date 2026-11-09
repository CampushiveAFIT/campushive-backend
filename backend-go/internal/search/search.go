package search

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/posts"

	"github.com/go-chi/chi/v5"
)

// SearchUser represents a user search hit.
type SearchUser struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// SearchUniversity represents a university search hit.
type SearchUniversity struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	LogoURL     *string `json:"logoUrl"`
}

// SearchCommunity represents a community search hit.
type SearchCommunity struct {
	ID           string  `json:"id"`
	UniversityID string  `json:"universityId"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	Description  *string `json:"description"`
	Type         string  `json:"type"`
	Visibility   string  `json:"visibility"`
}

// SearchEvent represents an event search hit.
type SearchEvent struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	StartsAt   time.Time `json:"startsAt"`
	Status     string    `json:"status"`
	Visibility string    `json:"visibility"`
}

// SearchPost represents a post search hit.
type SearchPost struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"createdAt"`
}

// SearchResults holds the unified search response.
type SearchResults struct {
	Users        []SearchUser       `json:"users"`
	Universities []SearchUniversity `json:"universities"`
	Communities  []SearchCommunity  `json:"communities"`
	Events       []SearchEvent      `json:"events"`
	Posts        []SearchPost       `json:"posts"`
}

// Handler handles search requests.
type Handler struct {
	db *database.DB
}

// NewHandler constructs a search Handler.
func NewHandler(db *database.DB) *Handler {
	return &Handler{db: db}
}

// Register mounts search routes.
func (h *Handler) Register(search chi.Router) {
	search.Get("/", h.search)
}

var validSearchTypes = map[string]bool{
	"all":          true,
	"users":        true,
	"universities": true,
	"communities":  true,
	"events":       true,
	"posts":        true,
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		httpx.WriteError(w, httpx.ValidationMessage("Search query must be at least 2 characters."))
		return
	}

	searchType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("type")))
	if searchType == "" {
		searchType = "all"
	}
	if !validSearchTypes[searchType] {
		httpx.WriteError(w, httpx.ValidationMessage("Invalid search type."))
		return
	}

	limit := 10
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		if n, err := strconv.Atoi(rawLimit); err == nil {
			if n < 1 {
				limit = 1
			} else if n > 25 {
				limit = 25
			} else {
				limit = n
			}
		}
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	results, err := h.execute(r.Context(), viewerID, q, searchType, limit)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, results)
}

func (h *Handler) execute(ctx context.Context, viewerID, q, searchType string, limit int) (SearchResults, error) {
	vctx, err := posts.LoadViewerContext(ctx, h.db, viewerID)
	if err != nil {
		return SearchResults{}, fmt.Errorf("load viewer context: %w", err)
	}

	results := SearchResults{
		Users:        []SearchUser{},
		Universities: []SearchUniversity{},
		Communities:  []SearchCommunity{},
		Events:       []SearchEvent{},
		Posts:        []SearchPost{},
	}

	want := func(t string) bool {
		return searchType == "all" || searchType == t
	}

	if want("users") {
		rows, err := h.db.Pool.Query(ctx, `
			SELECT "id", "displayName", "avatarUrl"
			FROM "User"
			WHERE "isActive" = true AND "displayName" ILIKE '%' || $1 || '%'
			ORDER BY "displayName" ASC
			LIMIT $2`, q, limit,
		)
		if err != nil {
			return SearchResults{}, fmt.Errorf("search users: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var u SearchUser
			if err := rows.Scan(&u.ID, &u.DisplayName, &u.AvatarURL); err != nil {
				return SearchResults{}, fmt.Errorf("scan user: %w", err)
			}
			isBlocked := false
			for _, b := range vctx.BlockedIDs {
				if b == u.ID {
					isBlocked = true
					break
				}
			}
			if !isBlocked {
				results.Users = append(results.Users, u)
			}
		}
	}

	if want("universities") {
		rows, err := h.db.Pool.Query(ctx, `
			SELECT "id", "name", "slug", "description", "logoUrl"
			FROM "University"
			WHERE "isActive" = true AND ("name" ILIKE '%' || $1 || '%' OR "description" ILIKE '%' || $1 || '%')
			ORDER BY "name" ASC
			LIMIT $2`, q, limit,
		)
		if err != nil {
			return SearchResults{}, fmt.Errorf("search universities: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var uni SearchUniversity
			if err := rows.Scan(&uni.ID, &uni.Name, &uni.Slug, &uni.Description, &uni.LogoURL); err != nil {
				return SearchResults{}, fmt.Errorf("scan university: %w", err)
			}
			results.Universities = append(results.Universities, uni)
		}
	}

	if want("communities") {
		rows, err := h.db.Pool.Query(ctx, `
			SELECT "id", "universityId", "name", "slug", "description", "type", "visibility"
			FROM "Community"
			WHERE "isActive" = true AND ("name" ILIKE '%' || $1 || '%' OR "description" ILIKE '%' || $1 || '%')
			ORDER BY "name" ASC
			LIMIT $2`, q, limit,
		)
		if err != nil {
			return SearchResults{}, fmt.Errorf("search communities: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var c SearchCommunity
			if err := rows.Scan(&c.ID, &c.UniversityID, &c.Name, &c.Slug, &c.Description, &c.Type, &c.Visibility); err != nil {
				return SearchResults{}, fmt.Errorf("scan community: %w", err)
			}

			visible := false
			if c.Visibility == "PUBLIC" {
				visible = true
			} else if c.Visibility == "UNIVERSITY" {
				for _, u := range vctx.UniversityIDs {
					if u == c.UniversityID {
						visible = true
						break
					}
				}
			} else if c.Visibility == "PRIVATE" {
				for _, comm := range vctx.CommunityIDs {
					if comm == c.ID {
						visible = true
						break
					}
				}
			}

			if visible {
				results.Communities = append(results.Communities, c)
			}
		}
	}

	if want("events") {
		rows, err := h.db.Pool.Query(ctx, `
			SELECT "id", "organizerId", "universityId", "communityId", "title", "startsAt", "status", "visibility"
			FROM "Event"
			WHERE "status" = 'PUBLISHED' AND ("title" ILIKE '%' || $1 || '%' OR "description" ILIKE '%' || $1 || '%')
			ORDER BY "startsAt" ASC, "id" ASC
			LIMIT $2`, q, limit,
		)
		if err != nil {
			return SearchResults{}, fmt.Errorf("search events: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id, organizerID, universityID string
			var communityID *string
			var title, status, visibility string
			var startsAt time.Time
			if err := rows.Scan(&id, &organizerID, &universityID, &communityID, &title, &startsAt, &status, &visibility); err != nil {
				return SearchResults{}, fmt.Errorf("scan event: %w", err)
			}

			isBlocked := false
			for _, b := range vctx.BlockedIDs {
				if b == organizerID {
					isBlocked = true
					break
				}
			}
			if isBlocked {
				continue
			}

			visible := false
			if organizerID == vctx.UserID || visibility == "PUBLIC" {
				visible = true
			} else if visibility == "UNIVERSITY" {
				for _, u := range vctx.UniversityIDs {
					if u == universityID {
						visible = true
						break
					}
				}
			} else if visibility == "COMMUNITY" && communityID != nil {
				for _, c := range vctx.CommunityIDs {
					if c == *communityID {
						visible = true
						break
					}
				}
			}

			if visible {
				results.Events = append(results.Events, SearchEvent{
					ID:         id,
					Title:      title,
					StartsAt:   startsAt,
					Status:     status,
					Visibility: visibility,
				})
			}
		}
	}

	if want("posts") {
		rows, err := h.db.Pool.Query(ctx, `
			SELECT "id", "authorId", "universityId", "communityId", "content", "visibility", "deletedAt", "createdAt"
			FROM "Post"
			WHERE "deletedAt" IS NULL AND "content" ILIKE '%' || $1 || '%'
			ORDER BY "createdAt" DESC, "id" DESC
			LIMIT $2`, q, limit,
		)
		if err != nil {
			return SearchResults{}, fmt.Errorf("search posts: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id, authorID, universityID string
			var communityID *string
			var content, visibility string
			var deletedAt *time.Time
			var createdAt time.Time
			if err := rows.Scan(&id, &authorID, &universityID, &communityID, &content, &visibility, &deletedAt, &createdAt); err != nil {
				return SearchResults{}, fmt.Errorf("scan post: %w", err)
			}

			if posts.CanViewPost(vctx, posts.PostAccess{
				AuthorID:     authorID,
				UniversityID: universityID,
				CommunityID:  communityID,
				Visibility:   visibility,
				DeletedAt:    deletedAt,
			}) {
				results.Posts = append(results.Posts, SearchPost{
					ID:        id,
					Content:   content,
					CreatedAt: createdAt,
				})
			}
		}
	}

	return results, nil
}

package routes

import (
	"campus-hive/backend-go/internal/auth"
	"campus-hive/backend-go/internal/communities"
	"campus-hive/backend-go/internal/email"
	"campus-hive/backend-go/internal/events"
	"campus-hive/backend-go/internal/feed"
	"campus-hive/backend-go/internal/graph"
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/messaging"
	"campus-hive/backend-go/internal/notifications"
	"campus-hive/backend-go/internal/openapi"
	"campus-hive/backend-go/internal/posts"
	"campus-hive/backend-go/internal/routex"
	"campus-hive/backend-go/internal/search"
	"campus-hive/backend-go/internal/universities"
	"campus-hive/backend-go/internal/users"

	"github.com/go-chi/chi/v5"
)

func Register(api chi.Router, deps httpserver.Dependencies, document *openapi.Document) {
	tokens := auth.NewTokenService(deps.Config.JWT)
	sender := email.NewLoggerSender(deps.Config.Email, deps.Logger)
	authService := auth.NewService(deps.DB, tokens, sender, deps.Config, deps.Logger)
	authHandler := auth.NewHandler(authService, auth.NewLimiter(deps.Limiter, deps.Logger))
	userHandler := users.NewHandler(users.NewService(deps.DB))
	univHandler := universities.NewHandler(deps.DB)
	commHandler := communities.NewHandler(deps.DB)
	graphHandler := graph.NewHandler(deps.DB)
	postsHandler := posts.NewHandler(deps.DB)
	notifHandler := notifications.NewHandler(deps.DB)
	msgHandler := messaging.NewHandler(deps.DB)
	eventsHandler := events.NewHandler(deps.DB)
	feedHandler := feed.NewHandler(deps.DB)
	searchHandler := search.NewHandler(deps.DB)

	// Every controller below is guarded by JwtAccessGuard in the legacy
	// reference, so each shared prefix is mounted once with its guard.
	guard := identity.Authenticate(tokens)
	usersAPI := routex.Authed(api, "/users", guard)
	universitiesAPI := routex.Authed(api, "/universities", guard)
	communitiesAPI := routex.Authed(api, "/communities", guard)
	postsAPI := routex.Authed(api, "/posts", guard)
	commentsAPI := routex.Authed(api, "/comments", guard)
	eventsAPI := routex.Authed(api, "/events", guard)
	ticketsAPI := routex.Authed(api, "/tickets", guard)
	notificationsAPI := routex.Authed(api, "/notifications", guard)
	conversationsAPI := routex.Authed(api, "/conversations", guard)
	feedAPI := routex.Authed(api, "/feed", guard)
	searchAPI := routex.Authed(api, "/search", guard)

	authHandler.Register(api, tokens)
	userHandler.Register(usersAPI)
	univHandler.Register(universitiesAPI, usersAPI)
	commHandler.Register(communitiesAPI)
	graphHandler.Register(usersAPI)
	postsHandler.Register(postsAPI, commentsAPI, usersAPI, universitiesAPI, communitiesAPI)
	notifHandler.Register(notificationsAPI, usersAPI)
	msgHandler.Register(conversationsAPI)
	eventsHandler.Register(eventsAPI, ticketsAPI, usersAPI, universitiesAPI, communitiesAPI)
	feedHandler.Register(feedAPI)
	searchHandler.Register(searchAPI)

	documentAuthPaths(document)
	documentUserPaths(document)
	documentUniversityPaths(document)
}

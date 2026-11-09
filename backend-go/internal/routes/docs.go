package routes

import "campus-hive/backend-go/internal/openapi"

func jsonBody(ref string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"$ref": "#/components/schemas/" + ref},
			},
		},
	}
}

func jsonResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"success": map[string]any{"type": "boolean"},
						"data":    map[string]any{},
					},
					"required": []any{"success", "data"},
				},
			},
		},
	}
}

func documentAuthPaths(document *openapi.Document) {
	bearer := []map[string][]any{{"access-token": {}}}

	document.AddPath("/auth/register", "post", openapi.Operation{
		Summary:     "Register a new account (does not authenticate)",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("RegisterRequest"),
		Responses: map[string]any{
			"201": jsonResponse("Account created; verification email sent."),
			"400": jsonResponse("Validation failed."),
			"409": jsonResponse("Email already registered."),
		},
	})
	document.AddPath("/auth/login", "post", openapi.Operation{
		Summary:     "Authenticate and receive access + refresh tokens",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("LoginRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Authenticated."),
			"401": jsonResponse("Invalid credentials."),
			"429": jsonResponse("Too many requests."),
		},
	})
	document.AddPath("/auth/refresh", "post", openapi.Operation{
		Summary:     "Rotate refresh token and receive new credentials",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("RefreshTokenRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Rotated."),
			"401": jsonResponse("Revoked, reused or expired refresh token."),
		},
	})
	document.AddPath("/auth/logout", "post", openapi.Operation{
		Summary:     "Revoke the current refresh-token session",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("RefreshTokenRequest"),
		Responses:   map[string]any{"200": jsonResponse("Session revoked.")},
	})
	document.AddPath("/auth/password/change", "post", openapi.Operation{
		Summary:     "Change password (invalidates all sessions)",
		Tags:        []string{"auth"},
		Security:    bearer,
		RequestBody: jsonBody("PasswordChangeRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Password changed."),
			"401": jsonResponse("Missing, invalid or wrong credentials."),
		},
	})
	document.AddPath("/auth/password/reset/request", "post", openapi.Operation{
		Summary:     "Request a password reset email (generic response)",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("EmailRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Request accepted."),
			"429": jsonResponse("Too many requests."),
		},
	})
	document.AddPath("/auth/password/reset/confirm", "post", openapi.Operation{
		Summary:     "Reset password with a valid reset token",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("PasswordResetConfirmRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Password reset."),
			"400": jsonResponse("Token invalid or expired."),
		},
	})
	document.AddPath("/auth/email/verify/request", "post", openapi.Operation{
		Summary:     "Request an email verification message",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("EmailRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Request accepted."),
			"429": jsonResponse("Too many requests."),
		},
	})
	document.AddPath("/auth/email/verify/confirm", "post", openapi.Operation{
		Summary:     "Confirm email verification with a valid token",
		Tags:        []string{"auth"},
		RequestBody: jsonBody("TokenRequest"),
		Responses: map[string]any{
			"200": jsonResponse("Email verified."),
			"400": jsonResponse("Token invalid or expired."),
		},
	})
}

func documentUserPaths(document *openapi.Document) {
	bearer := []map[string][]any{{"access-token": {}}}

	document.AddPath("/users/me", "get", openapi.Operation{
		Summary:   "Get the authenticated user profile",
		Tags:      []string{"users"},
		Security:  bearer,
		Responses: map[string]any{"200": jsonResponse("Current user profile."), "401": jsonResponse("Missing or invalid access token.")},
	})
	document.AddPath("/users/me", "patch", openapi.Operation{
		Summary:     "Update displayName, bio, or avatarUrl",
		Tags:        []string{"users"},
		Security:    bearer,
		RequestBody: jsonBody("UpdateProfileRequest"),
		Responses:   map[string]any{"200": jsonResponse("Updated profile.")},
	})
}

func documentUniversityPaths(document *openapi.Document) {
	bearer := []map[string][]any{{"access-token": {}}}

	// Universities
	document.AddPath("/universities", "get", openapi.Operation{
		Summary: "List active universities", Tags: []string{"universities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated universities."), "401": jsonResponse("Unauthorized.")},
	})
	document.AddPath("/universities/{slug}", "get", openapi.Operation{
		Summary: "University detail by slug", Tags: []string{"universities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("University detail."), "404": jsonResponse("Not found.")},
	})
	document.AddPath("/universities/{slug}/communities", "get", openapi.Operation{
		Summary: "List university communities (visibility-filtered)", Tags: []string{"universities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated communities."), "404": jsonResponse("University not found.")},
	})
	document.AddPath("/universities/{slug}/posts", "get", openapi.Operation{
		Summary: "List university posts (members only)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated posts."), "403": jsonResponse("Not a member."), "404": jsonResponse("University not found.")},
	})

	// Campus identity
	document.AddPath("/users/me/university", "get", openapi.Operation{
		Summary: "Get active university membership", Tags: []string{"universities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Membership."), "404": jsonResponse("No active membership.")},
	})
	document.AddPath("/users/me/university", "post", openapi.Operation{
		Summary: "Join or switch university", Tags: []string{"universities"}, Security: bearer,
		RequestBody: jsonBody("JoinUniversityRequest"),
		Responses:   map[string]any{"200": jsonResponse("Membership."), "400": jsonResponse("Validation error."), "404": jsonResponse("University not found.")},
	})
	document.AddPath("/users/me/university", "patch", openapi.Operation{
		Summary: "Switch primary university", Tags: []string{"universities"}, Security: bearer,
		RequestBody: jsonBody("JoinUniversityRequest"),
		Responses:   map[string]any{"200": jsonResponse("Membership."), "400": jsonResponse("Validation error."), "404": jsonResponse("University not found.")},
	})

	// Communities
	document.AddPath("/communities", "post", openapi.Operation{
		Summary: "Create a community in your university", Tags: []string{"communities"}, Security: bearer,
		RequestBody: jsonBody("CreateCommunityRequest"),
		Responses:   map[string]any{"201": jsonResponse("Community."), "400": jsonResponse("Validation."), "403": jsonResponse("Forbidden."), "409": jsonResponse("Slug conflict.")},
	})
	document.AddPath("/communities/{id}", "get", openapi.Operation{
		Summary: "Community detail (visibility-enforced)", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{community, myMembership}."), "403": jsonResponse("Forbidden."), "404": jsonResponse("Not found.")},
	})
	document.AddPath("/communities/{id}", "patch", openapi.Operation{
		Summary: "Update community (owner only)", Tags: []string{"communities"}, Security: bearer,
		RequestBody: jsonBody("UpdateCommunityRequest"),
		Responses:   map[string]any{"200": jsonResponse("Updated community."), "403": jsonResponse("Forbidden."), "409": jsonResponse("Slug conflict.")},
	})
	document.AddPath("/communities/{id}", "delete", openapi.Operation{
		Summary: "Deactivate community (owner only)", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Community view."), "403": jsonResponse("Forbidden.")},
	})
	document.AddPath("/communities/{id}/join", "post", openapi.Operation{
		Summary: "Join a community", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Member view."), "403": jsonResponse("Private/banned/not a uni member.")},
	})
	document.AddPath("/communities/{id}/leave", "delete", openapi.Operation{
		Summary: "Leave a community", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{left: bool}."), "400": jsonResponse("Owner cannot leave.")},
	})
	document.AddPath("/communities/{id}/members", "get", openapi.Operation{
		Summary: "List community members", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated members."), "403": jsonResponse("Visibility restriction.")},
	})
	document.AddPath("/communities/{id}/members/{userId}", "patch", openapi.Operation{
		Summary: "Update member role (owner only)", Tags: []string{"communities"}, Security: bearer,
		RequestBody: jsonBody("UpdateMemberRoleRequest"),
		Responses:   map[string]any{"200": jsonResponse("Member view."), "400": jsonResponse("Owner role immutable."), "403": jsonResponse("Forbidden.")},
	})
	document.AddPath("/communities/{id}/members/{userId}", "delete", openapi.Operation{
		Summary: "Remove (ban) a member", Tags: []string{"communities"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Banned member view."), "403": jsonResponse("Forbidden.")},
	})
	document.AddPath("/communities/{id}/posts", "get", openapi.Operation{
		Summary: "List community posts", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated posts."), "403": jsonResponse("Private community.")},
	})

	// Follow/block
	document.AddPath("/users/{id}/follow", "post", openapi.Operation{
		Summary: "Follow a user (idempotent)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{following: true}."), "400": jsonResponse("Self-follow."), "403": jsonResponse("Blocked."), "404": jsonResponse("User not found.")},
	})
	document.AddPath("/users/{id}/follow", "delete", openapi.Operation{
		Summary: "Unfollow a user (idempotent)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{following: false}.")},
	})
	document.AddPath("/users/{id}/followers", "get", openapi.Operation{
		Summary: "List followers (blocked users excluded)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated followers.")},
	})
	document.AddPath("/users/{id}/following", "get", openapi.Operation{
		Summary: "List following (blocked users excluded)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated following.")},
	})
	document.AddPath("/users/{id}/block", "post", openapi.Operation{
		Summary: "Block a user (severs follows both ways)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{blocked: true}."), "400": jsonResponse("Self-block."), "404": jsonResponse("User not found.")},
	})
	document.AddPath("/users/{id}/block", "delete", openapi.Operation{
		Summary: "Unblock a user (idempotent)", Tags: []string{"users"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{blocked: false}.")},
	})
	document.AddPath("/users/{id}/posts", "get", openapi.Operation{
		Summary: "List an author's visible posts", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated posts."), "404": jsonResponse("User not found.")},
	})

	// Posts
	document.AddPath("/posts", "post", openapi.Operation{
		Summary: "Create a post", Tags: []string{"posts"}, Security: bearer,
		RequestBody: jsonBody("CreatePostRequest"),
		Responses:   map[string]any{"201": jsonResponse("Post."), "400": jsonResponse("Validation."), "403": jsonResponse("No university membership.")},
	})
	document.AddPath("/posts/{id}", "get", openapi.Operation{
		Summary: "Post detail (visibility-enforced)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Post with likedByMe."), "404": jsonResponse("Not found/invisible.")},
	})
	document.AddPath("/posts/{id}", "patch", openapi.Operation{
		Summary: "Update post (author only)", Tags: []string{"posts"}, Security: bearer,
		RequestBody: jsonBody("UpdatePostRequest"),
		Responses:   map[string]any{"200": jsonResponse("Updated post."), "403": jsonResponse("Not author.")},
	})
	document.AddPath("/posts/{id}", "delete", openapi.Operation{
		Summary: "Soft-delete post (author only)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{deleted: true}."), "403": jsonResponse("Not author.")},
	})
	document.AddPath("/posts/{id}/like", "post", openapi.Operation{
		Summary: "Like a post (idempotent)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{liked: true, likeCount}."), "404": jsonResponse("Not found/invisible.")},
	})
	document.AddPath("/posts/{id}/like", "delete", openapi.Operation{
		Summary: "Unlike a post (idempotent)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{liked: false, likeCount}.")},
	})
	document.AddPath("/posts/{id}/comments", "post", openapi.Operation{
		Summary: "Comment on a post", Tags: []string{"posts"}, Security: bearer,
		RequestBody: jsonBody("CreateCommentRequest"),
		Responses:   map[string]any{"201": jsonResponse("Comment."), "404": jsonResponse("Post not found/invisible.")},
	})
	document.AddPath("/posts/{id}/comments", "get", openapi.Operation{
		Summary: "List post comments (oldest first)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("Paginated comments."), "404": jsonResponse("Post not found/invisible.")},
	})
	document.AddPath("/comments/{id}", "patch", openapi.Operation{
		Summary: "Update comment (author only)", Tags: []string{"posts"}, Security: bearer,
		RequestBody: jsonBody("UpdateCommentRequest"),
		Responses:   map[string]any{"200": jsonResponse("Updated comment."), "404": jsonResponse("Not found or not author.")},
	})
	document.AddPath("/comments/{id}", "delete", openapi.Operation{
		Summary: "Delete comment (author or post owner)", Tags: []string{"posts"}, Security: bearer,
		Responses: map[string]any{"200": jsonResponse("{deleted: true}."), "403": jsonResponse("Forbidden."), "404": jsonResponse("Not found.")},
	})
}

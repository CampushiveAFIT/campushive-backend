package auth

import (
	"net/http"

	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/validate"
)

const (
	msgRegisterEmail  = "email must be a valid email address"
	msgPasswordMin    = "password must be at least 8 characters"
	msgPasswordRule   = "password must contain at least one lowercase letter, one uppercase letter, and one digit"
	msgNewMin         = "newPassword must be at least 8 characters"
	msgNewRule        = "newPassword must contain at least one lowercase letter, one uppercase letter, and one digit"
	registerMaxPass   = 128
	registerMaxName   = 120
	registerMaxEmail  = 255
	passwordMaxLength = 128
	tokenMinLength    = 32
	tokenMaxLength    = 256
)

type RegisterRequest struct {
	Email       string
	Password    string
	DisplayName *string
}

func DecodeRegister(r *http.Request) (RegisterRequest, error) {
	body, err := httpx.DecodeBody(r, "email", "password", "displayName")
	if err != nil {
		return RegisterRequest{}, err
	}
	email := body.String("email").Transform(validate.LowerTrim)
	password := body.String("password")
	displayName := body.String("displayName").Transform(validate.Trim).Optional()

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(email, registerMaxEmail)
	errors.Email(email, msgRegisterEmail)
	errors.StrongPassword(password, msgPasswordRule)
	errors.MaxLength(password, registerMaxPass)
	errors.MinLength(password, 8, msgPasswordMin)
	errors.IsString(password)
	errors.MaxLength(displayName, registerMaxName)
	errors.IsString(displayName)
	if len(errors) > 0 {
		return RegisterRequest{}, httpx.ValidationError(errors)
	}
	request := RegisterRequest{Email: email.Value(), Password: password.Value()}
	if displayName.Present() {
		name := displayName.Value()
		request.DisplayName = &name
	}
	return request, nil
}

type LoginRequest struct {
	Email    string
	Password string
}

func DecodeLogin(r *http.Request) (LoginRequest, error) {
	body, err := httpx.DecodeBody(r, "email", "password")
	if err != nil {
		return LoginRequest{}, err
	}
	email := body.String("email").Transform(validate.LowerTrim)
	password := body.String("password")

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(email, registerMaxEmail)
	errors.Email(email, "")
	errors.MaxLength(password, passwordMaxLength)
	errors.IsString(password)
	if len(errors) > 0 {
		return LoginRequest{}, httpx.ValidationError(errors)
	}
	return LoginRequest{Email: email.Value(), Password: password.Value()}, nil
}

type RefreshTokenRequest struct {
	RefreshToken string
}

func DecodeRefreshToken(r *http.Request) (RefreshTokenRequest, error) {
	body, err := httpx.DecodeBody(r, "refreshToken")
	if err != nil {
		return RefreshTokenRequest{}, err
	}
	token := body.String("refreshToken")

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(token, tokenMaxLength)
	errors.MinLength(token, tokenMinLength, "")
	errors.IsString(token)
	if len(errors) > 0 {
		return RefreshTokenRequest{}, httpx.ValidationError(errors)
	}
	return RefreshTokenRequest{RefreshToken: token.Value()}, nil
}

type PasswordChangeRequest struct {
	CurrentPassword string
	NewPassword     string
}

func DecodePasswordChange(r *http.Request) (PasswordChangeRequest, error) {
	body, err := httpx.DecodeBody(r, "currentPassword", "newPassword")
	if err != nil {
		return PasswordChangeRequest{}, err
	}
	current := body.String("currentPassword")
	next := body.String("newPassword")

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(current, passwordMaxLength)
	errors.IsString(current)
	errors.StrongPassword(next, msgNewRule)
	errors.MaxLength(next, passwordMaxLength)
	errors.MinLength(next, 8, msgNewMin)
	errors.IsString(next)
	if len(errors) > 0 {
		return PasswordChangeRequest{}, httpx.ValidationError(errors)
	}
	return PasswordChangeRequest{CurrentPassword: current.Value(), NewPassword: next.Value()}, nil
}

type PasswordResetRequest struct {
	Email string
}

func DecodePasswordResetRequest(r *http.Request) (PasswordResetRequest, error) {
	body, err := httpx.DecodeBody(r, "email")
	if err != nil {
		return PasswordResetRequest{}, err
	}
	email := body.String("email").Transform(validate.LowerTrim)

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(email, registerMaxEmail)
	errors.Email(email, "")
	if len(errors) > 0 {
		return PasswordResetRequest{}, httpx.ValidationError(errors)
	}
	return PasswordResetRequest{Email: email.Value()}, nil
}

type PasswordResetConfirmRequest struct {
	Token       string
	NewPassword string
}

func DecodePasswordResetConfirm(r *http.Request) (PasswordResetConfirmRequest, error) {
	body, err := httpx.DecodeBody(r, "token", "newPassword")
	if err != nil {
		return PasswordResetConfirmRequest{}, err
	}
	token := body.String("token")
	next := body.String("newPassword")

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(token, tokenMaxLength)
	errors.MinLength(token, tokenMinLength, "")
	errors.IsString(token)
	errors.StrongPassword(next, msgNewRule)
	errors.MaxLength(next, passwordMaxLength)
	errors.MinLength(next, 8, msgNewMin)
	errors.IsString(next)
	if len(errors) > 0 {
		return PasswordResetConfirmRequest{}, httpx.ValidationError(errors)
	}
	return PasswordResetConfirmRequest{Token: token.Value(), NewPassword: next.Value()}, nil
}

type EmailVerifyRequest struct {
	Email string
}

func DecodeEmailVerifyRequest(r *http.Request) (EmailVerifyRequest, error) {
	body, err := httpx.DecodeBody(r, "email")
	if err != nil {
		return EmailVerifyRequest{}, err
	}
	email := body.String("email").Transform(validate.LowerTrim)

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(email, registerMaxEmail)
	errors.Email(email, "")
	if len(errors) > 0 {
		return EmailVerifyRequest{}, httpx.ValidationError(errors)
	}
	return EmailVerifyRequest{Email: email.Value()}, nil
}

type EmailVerifyConfirmRequest struct {
	Token string
}

func DecodeEmailVerifyConfirm(r *http.Request) (EmailVerifyConfirmRequest, error) {
	body, err := httpx.DecodeBody(r, "token")
	if err != nil {
		return EmailVerifyConfirmRequest{}, err
	}
	token := body.String("token")

	var errors validate.Errors
	errors.Whitelist(body.Unknown())
	errors.MaxLength(token, tokenMaxLength)
	errors.MinLength(token, tokenMinLength, "")
	errors.IsString(token)
	if len(errors) > 0 {
		return EmailVerifyConfirmRequest{}, httpx.ValidationError(errors)
	}
	return EmailVerifyConfirmRequest{Token: token.Value()}, nil
}

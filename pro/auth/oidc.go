package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gravitl/netmaker/database"
	"github.com/gravitl/netmaker/logger"
	"github.com/gravitl/netmaker/logic"
	"github.com/gravitl/netmaker/models"
	proLogic "github.com/gravitl/netmaker/pro/logic"
	"github.com/gravitl/netmaker/servercfg"
	"golang.org/x/oauth2"
)

const OIDC_TIMEOUT = 10 * time.Second

var oidc_functions = map[string]interface{}{
	init_provider:   initOIDC,
	get_user_info:   getOIDCUserInfo,
	handle_callback: handleOIDCCallback,
	handle_login:    handleOIDCLogin,
	verify_user:     verifyOIDCUser,
}

var oidc_verifier *oidc.IDTokenVerifier

// == handle OIDC authentication here ==

func initOIDC(redirectURL string, clientID string, clientSecret string, issuer string) {
	ctx, cancel := context.WithTimeout(context.Background(), OIDC_TIMEOUT)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		logger.Log(1, "error when initializing OIDC provider with issuer \""+issuer+"\"", err.Error())
		return
	}

	oidc_verifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	auth_provider = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}
}

func handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	var oauth_state_string = logic.RandomString(user_signin_length)
	if auth_provider == nil {
		handleOauthNotConfigured(w)
		return
	}

	if err := logic.SetState(oauth_state_string); err != nil {
		handleOauthNotConfigured(w)
		return
	}
	var url = auth_provider.AuthCodeURL(oauth_state_string)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleOIDCCallback(w http.ResponseWriter, r *http.Request) {

	var rState, rCode = getStateAndCode(r)

	var content, err = getOIDCUserInfo(rState, rCode)
	if err != nil {
		logger.Log(1, "error when getting user info from callback:", err.Error())
		if strings.Contains(err.Error(), "invalid oauth state") {
			handleOauthNotValid(w)
			return
		}
		handleOauthNotConfigured(w)
		return
	}
	var inviteExists bool
	// check if invite exists for User
	in, err := logic.GetUserInvite(content.Email)
	if err == nil {
		inviteExists = true
	}
	// check if user approval is already pending
	if !inviteExists && logic.IsPendingUser(content.Email) {
		handleOauthUserSignUpApprovalPending(w)
		return
	}

	user, err := logic.GetUser(content.Email)
	if err != nil {
		if database.IsEmptyRecord(err) { // user must not exist, so try to make one
			if inviteExists {
				// create user
				user, err := proLogic.PrepareOauthUserFromInvite(in)
				if err != nil {
					logic.ReturnErrorResponse(w, r, logic.FormatError(err, "internal"))
					return
				}
				user.ExternalIdentityProviderID = string(content.ID)
				if err = logic.CreateUser(&user); err != nil {
					handleSomethingWentWrong(w)
					return
				}
				logic.DeleteUserInvite(user.UserName)
				logic.DeletePendingUser(content.Email)
			} else {
				if !isEmailAllowed(content.Email) {
					handleOauthUserNotAllowedToSignUp(w)
					return
				}
				err = logic.InsertPendingUser(&models.User{
					UserName:                   content.Email,
					ExternalIdentityProviderID: string(content.ID),
					AuthType:                   models.OAuth,
				})
				if err != nil {
					handleSomethingWentWrong(w)
					return
				}
				handleFirstTimeOauthUserSignUp(w)
				return
			}
		} else {
			handleSomethingWentWrong(w)
			return
		}
	} else {
		// if user exists, then ensure user's auth type is
		// oauth before proceeding.
		if user.AuthType == models.BasicAuth {
			logger.Log(0, "invalid auth type: basic_auth")
			handleAuthTypeMismatch(w)
			return
		}
	}

	user, err = logic.GetUser(content.Email)
	if err != nil {
		handleOauthUserNotFound(w)
		return
	}

	if user.AccountDisabled {
		handleUserAccountDisabled(w)
		return
	}

	userRole, err := logic.GetRole(user.PlatformRoleID)
	if err != nil {
		handleSomethingWentWrong(w)
		return
	}
	if userRole.DenyDashboardAccess {
		handleOauthUserNotAllowed(w)
		return
	}
	var newPass, fetchErr = logic.FetchPassValue("")
	if fetchErr != nil {
		return
	}
	// send a netmaker jwt token
	var authRequest = models.UserAuthParams{
		UserName: content.Email,
		Password: newPass,
	}

	var jwt, jwtErr = logic.VerifyAuthRequest(authRequest)
	if jwtErr != nil {
		logger.Log(1, "could not parse jwt for user", authRequest.UserName, jwtErr.Error())
		return
	}
	logic.LogEvent(&models.Event{
		Action: models.Login,
		Source: models.Subject{
			ID:   user.UserName,
			Name: user.UserName,
			Type: models.UserSub,
		},
		TriggeredBy: user.UserName,
		Target: models.Subject{
			ID:   models.DashboardSub.String(),
			Name: models.DashboardSub.String(),
			Type: models.DashboardSub,
			Info: user,
		},
		Origin: models.Dashboard,
	})
	logger.Log(1, "completed OIDC OAuth signin in for", content.Email)
	http.Redirect(w, r, servercfg.GetFrontendURL()+"/login?login="+jwt+"&user="+content.Email, http.StatusPermanentRedirect)
}

func getOIDCUserInfo(state string, code string) (u *OAuthUser, e error) {
	oauth_state_string, isValid := logic.IsStateValid(state)
	logger.Log(3, "using oauth state string:,", oauth_state_string)
	logger.Log(3, "            state string:,", state)
	if (!isValid || state != oauth_state_string) && !isStateCached(state) {
		return nil, fmt.Errorf("invalid oauth state")
	}

	defer func() {
		if p := recover(); p != nil {
			e = fmt.Errorf("getOIDCUserInfo panic: %v", p)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), OIDC_TIMEOUT)
	defer cancel()

	oauth2Token, err := auth_provider.Exchange(ctx, code, oauth2.SetAuthURLParam("prompt", "login"))
	if err != nil {
		return nil, fmt.Errorf("failed to exchange oauth2 token using code \"%s\"", code)
	}

	rawIDToken, ok := oauth2Token.Extra("id_token").(string)
	if !ok {
		return nil, fmt.Errorf("failed to get raw id_token from oauth2 token")
	}

	idToken, err := oidc_verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("failed to verify raw id_token: \"%s\"", err.Error())
	}

	u = &OAuthUser{}
	if err := idToken.Claims(u); err != nil {
		e = fmt.Errorf("error when claiming OIDCUser: \"%s\"", err.Error())
	}

	u.ID = StringOrInt(idToken.Subject)

	return
}

func verifyOIDCUser(token *oauth2.Token) bool {
	return token.Valid()
}

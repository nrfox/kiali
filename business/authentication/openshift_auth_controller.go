package authentication

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/kiali/kiali/business"
	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/log"
	"github.com/kiali/kiali/util"
)

// openshiftSessionPayload holds the data that will be persisted in the SessionStore
// in order to be able to maintain the session of the user across requests.
type openshiftSessionPayload struct {
	oauth2.Token
}

// OpenshiftAuthController contains the backing logic to implement
// Kiali's "openshift" authentication strategy. This authentication
// strategy is basically an implementation of OAuth's implicit flow
// with the specifics of OpenShift.
//
// Alternatively, it is possible that 3rd-parties are controlling
// the session. For these cases, Kiali can receive an OpenShift token
// via the "Authorization" HTTP Header or via the "oauth_token"
// URL parameter. Token received from 3rd parties are not persisted
// with the active Kiali's persistor, because that would collide and
// replace an existing Kiali session. So, it is assumed that the 3rd-party
// has its own persistence system (similarly to how 'header' auth works).
type OpenshiftAuthController struct {
	conf           *config.Config
	openshiftOAuth *business.OpenshiftOAuthService
	oAuthConfig    *oauth2.Config
	secureCookie   bool
	// SessionStore persists the session between HTTP requests.
	SessionStore         SessionPersistor
	oAuthServerTLSConfig *tls.Config
}

// NewOpenshiftAuthController initializes a new controller for handling OpenShift authentication, with the
// given persistor and the given businessInstantiator. The businessInstantiator can be nil and
// the initialized contoller will use the business.Get function.
func NewOpenshiftAuthController(persistor SessionPersistor, openshiftOAuth *business.OpenshiftOAuthService, conf *config.Config) (*OpenshiftAuthController, error) {
	oAuthServer, err := openshiftOAuth.GetOAuthAuthorizationServer(context.TODO())
	if err != nil {
		log.Errorf("Could not get OAuth server: %v", err)
		return nil, err
	}

	oAuthClient, err := openshiftOAuth.GetOAuthClient(context.TODO())
	if err != nil {
		log.Errorf("Could not get OAuth client: %v", err)
		return nil, err
	}

	if len(oAuthClient.RedirectURIs) == 0 {
		return nil, fmt.Errorf("oAuth client has no redirect URIs")
	}

	oAuthConfig := &oauth2.Config{
		ClientID:    oAuthClient.Name,
		RedirectURL: oAuthClient.RedirectURIs[0],
		Scopes:      []string{"user:full"},
		Endpoint: oauth2.Endpoint{
			AuthURL:  oAuthServer.AuthorizationEndpoint,
			TokenURL: oAuthServer.TokenEndpoint,
		},
	}

	certPool, err := business.OpenshiftAuthCACertPool(conf)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{RootCAs: certPool}
	return &OpenshiftAuthController{
		conf:                 conf,
		oAuthConfig:          oAuthConfig,
		oAuthServerTLSConfig: tlsConfig,
		openshiftOAuth:       openshiftOAuth,
		secureCookie:         conf.IsServerHTTPS() || strings.HasPrefix(oAuthConfig.RedirectURL, "https:"),
		SessionStore:         persistor,
	}, nil
}

// PostRoutes adds the additional endpoints needed on the Kiali's router
// in order to properly enable OpenId authentication. Only one new route is added to
// do a redirection from Kiali to the OpenId server to initiate authentication.
func (c OpenshiftAuthController) PostRoutes(router *mux.Router) {
	// swagger:route GET /auth/openid_redirect auth openidRedirect
	// ---
	// Endpoint to redirect the browser of the user to the authentication
	// endpoint of the configured OpenId provider.
	//
	//     Consumes:
	//     - application/json
	//
	//     Produces:
	//     - application/html
	//
	//     Schemes: http, https
	//
	// responses:
	//      500: internalError
	//      200: noContent
	router.
		Methods("GET").
		Path("/api/auth/openshift_redirect").
		Name("OpenShiftAuthRedirect").
		HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			verifier := oauth2.GenerateVerifier() // Store in the session cookie

			nowTime := util.Clock.Now()
			expirationTime := nowTime.Add(time.Duration(c.conf.Auth.OpenId.AuthenticationTimeout) * time.Second)
			// nonce cookie stores the verifier.
			nonceCookie := http.Cookie{
				Expires:  expirationTime,
				HttpOnly: true,
				Secure:   c.secureCookie,
				Name:     OpenIdNonceCookieName,
				Path:     c.conf.Server.WebRoot,
				SameSite: http.SameSiteLaxMode,
				Value:    verifier,
			}
			http.SetCookie(w, &nonceCookie)
			// Redirect user to consent page to ask for permission
			// for the scopes specified above.
			url := c.oAuthConfig.AuthCodeURL("", oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier))
			http.Redirect(w, r, url, http.StatusFound)
		})
}

func (c OpenshiftAuthController) GetAuthCallbackHandler(fallbackHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCookie, err := r.Cookie(OpenIdNonceCookieName)
		if err != nil {
			log.Debugf("Not handling OAuth code flow authentication: could not get the nonce cookie: %v", err)
			fallbackHandler.ServeHTTP(w, r)
			return
		}

		code := r.FormValue("code")
		if code == "" {
			log.Debugf("Not handling OAuth code flow authentication: could not get the code: %v", err)
			fallbackHandler.ServeHTTP(w, r)
			return
		}

		// If we get here then the request IS a callback from the OpenId provider.

		webRoot := c.conf.Server.WebRoot
		webRootWithSlash := webRoot + "/"

		// Use the custom HTTP client when requesting a token.
		httpClient := &http.Client{Transport: &http.Transport{TLSClientConfig: c.oAuthServerTLSConfig}}
		ctx := context.WithValue(r.Context(), oauth2.HTTPClient, httpClient)

		tok, err := c.oAuthConfig.Exchange(ctx, code, oauth2.VerifierOption(nonceCookie.Value))
		if err != nil {
			log.Errorf("Authentication rejected: Unable to exchange the code for a token: %v", err)
			http.Redirect(w, r, fmt.Sprintf("%s?openid_error=%s", webRootWithSlash, url.QueryEscape(err.Error())), http.StatusFound)
			return
		}

		if err := c.SessionStore.CreateSession(r, w, config.AuthStrategyOpenshift, tok.Expiry, tok); err != nil {
			log.Errorf("Authentication rejected: Could not create the session: %v", err)
			http.Redirect(w, r, fmt.Sprintf("%s?openid_error=%s", webRootWithSlash, url.QueryEscape(err.Error())), http.StatusFound)
			return
		}

		// Delete the nonce cookie since we no longer need it.
		deleteNonceCookie := http.Cookie{
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			Name:     OpenIdNonceCookieName,
			Path:     c.conf.Server.WebRoot,
			Secure:   c.secureCookie,
			SameSite: http.SameSiteStrictMode,
			Value:    "",
		}
		http.SetCookie(w, &deleteNonceCookie)

		// Use the authorization code that is pushed to the redirect
		// Let's redirect (remove the openid params) to let the Kiali-UI to boot
		http.Redirect(w, r, webRootWithSlash, http.StatusFound)
	})
}

// Authenticate handles an HTTP request that contains the access_token, expires_in URL parameters. The access_token
// should be the token that was obtained from the OpenShift OAuth server and expires_in is the expiration date-time
// of the token. The token is validated by obtaining the information user tied to it. Although RBAC is always assumed
// when using OpenShift, privileges are not checked here.
func (o OpenshiftAuthController) Authenticate(r *http.Request, w http.ResponseWriter) (*UserSessionData, error) {
	return nil, fmt.Errorf("support for OAuth's implicit flow has been removed")
}

// ValidateSession restores a session previously created by the Authenticate function. The user token (access_token)
// is revalidated by re-fetching user info from the cluster, to ensure that the token hasn't been revoked.
// If the session is still valid, a populated UserSessionData is returned. Otherwise, nil is returned.
func (o OpenshiftAuthController) ValidateSession(r *http.Request, w http.ResponseWriter) (*UserSessionData, error) {
	var token string
	var expires time.Time

	// In OpenShift auth, it is possible that a session is started by a 3rd party. If that's the case, Kiali
	// can receive the OpenShift token of the session via HTTP Headers of via a URL Query string parameter.
	// HTTP Headers have priority over URL parameters. If a token is received via some of these means,
	// then the received session has priority over the Kiali initiated session (stored in cookies).
	if authHeader := r.Header.Get("Authorization"); len(authHeader) != 0 && strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
		expires = util.Clock.Now().Add(time.Second * time.Duration(config.Get().LoginToken.ExpirationSeconds))
	} else if authToken := r.URL.Query().Get("oauth_token"); len(authToken) != 0 {
		token = strings.TrimSpace(authToken)
		expires = util.Clock.Now().Add(time.Second * time.Duration(config.Get().LoginToken.ExpirationSeconds))
	} else {
		sPayload := openshiftSessionPayload{}
		sData, err := o.SessionStore.ReadSession(r, w, &sPayload)
		if err != nil {
			log.Warningf("Could not read the openshift session: %v", err)
			return nil, nil
		}
		if sData == nil {
			return nil, nil
		}

		// The Openshift token must be present
		if len(sPayload.AccessToken) == 0 {
			log.Warning("Session is invalid: the Openshift token is absent")
			return nil, nil
		}

		token = sPayload.AccessToken
		expires = sData.ExpiresOn
	}

	user, err := o.openshiftOAuth.GetUserInfo(r.Context(), token)
	if err == nil {
		// Internal header used to propagate the subject of the request for audit purposes
		r.Header.Add("Kiali-User", user.Name)
		return &UserSessionData{
			ExpiresOn: expires,
			Username:  user.Name,
			AuthInfo:  &api.AuthInfo{Token: token},
		}, nil
	}

	log.Warningf("Token error: %v", err)
	return nil, nil
}

// TerminateSession session created by the Authenticate function.
// To properly clean the session, the OpenShift access_token is revoked/deleted by making a call
// to the relevant OpenShift API. If this process fails, the session is not cleared and an error
// is returned.
// The cleanup is done assuming the access_token was issued to be used only in Kiali.
func (o OpenshiftAuthController) TerminateSession(r *http.Request, w http.ResponseWriter) error {
	sPayload := openshiftSessionPayload{}
	sData, err := o.SessionStore.ReadSession(r, w, &sPayload)
	if err != nil {
		return TerminateSessionError{
			Message:    fmt.Sprintf("There is no active openshift session: %v", err),
			HttpStatus: http.StatusUnauthorized,
		}
	}
	if sData == nil {
		return TerminateSessionError{
			Message:    "logout problem: no session exists.",
			HttpStatus: http.StatusInternalServerError,
		}
	}

	// The Openshift token must be present
	if len(sPayload.AccessToken) == 0 {
		return TerminateSessionError{
			Message:    "Cannot logout: the Openshift token is absent from the session",
			HttpStatus: http.StatusInternalServerError,
		}
	}

	err = o.openshiftOAuth.Logout(r.Context(), sPayload.AccessToken)
	if err != nil {
		return TerminateSessionError{
			Message:    fmt.Sprintf("Could not log out of OpenShift: %v", err),
			HttpStatus: http.StatusInternalServerError,
		}
	}

	o.SessionStore.TerminateSession(r, w)
	return nil
}

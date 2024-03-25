package business

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
)

const defaultAuthRequestTimeout = 10 * time.Second

func NewOpenshiftOAuthService(conf *config.Config, kialiSAClient kubernetes.ClientInterface) (*OpenshiftOAuthService, error) {
	tlsConfig := &tls.Config{}
	if customCA := conf.Auth.OpenShift.CustomCA; customCA != "" {
		log.Debugf("using custom CA for Openshift OAuth [%v]", customCA)
		certPool := x509.NewCertPool()
		decodedCustomCA, err := base64.URLEncoding.DecodeString(customCA)
		if err != nil {
			return nil, fmt.Errorf("error decoding custom CA certificates: %s", err)
		}
		if !certPool.AppendCertsFromPEM(decodedCustomCA) {
			return nil, fmt.Errorf("failed to add custom CA certificates: %s", err)
		}
		tlsConfig = &tls.Config{RootCAs: certPool}
	} else if !conf.Auth.OpenShift.UseSystemCA {
		log.Debugf("Using serviceaccount CA for Openshift OAuth")
		certPool := x509.NewCertPool()
		cert, err := os.ReadFile("/run/secrets/kubernetes.io/serviceaccount/ca.crt")
		if err != nil {
			return nil, fmt.Errorf("failed to get root CA certificates: %s", err)
		}
		certPool.AppendCertsFromPEM(cert)
		tlsConfig = &tls.Config{RootCAs: certPool}
	} else {
		log.Debugf("Using system CA for Openshift OAuth")
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	if conf.Auth.OpenShift.AuthTimeout != 0 {
		client.Timeout = time.Duration(conf.Auth.OpenShift.AuthTimeout) * time.Second
	} else {
		client.Timeout = defaultAuthRequestTimeout
	}

	return &OpenshiftOAuthService{
		conf:          conf,
		httpclient:    client,
		kialiSAClient: kialiSAClient,
	}, nil
}

type OpenshiftOAuthService struct {
	// TODO: Support multi-cluster
	conf          *config.Config
	httpclient    *http.Client
	kialiSAClient kubernetes.ClientInterface
}

type OAuthMetadata struct {
	AuthorizationEndpoint string `json:"authorizationEndpoint"`
	LogoutEndpoint        string `json:"logoutEndpoint"`
	LogoutRedirect        string `json:"logoutRedirect"`
}

// Structure that's returned by the openshift oauth authorization server.
// It defaults to following the snake_case format, so we parse it to something
// more usable on our side.
type OAuthAuthorizationServer struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	Issuer                string `json:"issuer"`
}

type OAuthUser struct {
	Metadata OAuthUserMetadata `json:"metadata"`
}

type OAuthUserMetadata struct {
	Name string `json:"name"`
}

// TODO: Where to fetch redirect uri? From the route?
// Need to move that autodiscovery into the kiali server.
// Otherwise we'll need per cluster config which will be a pain.

// // The logout endpoint on the OpenShift OAuth Server
// metadata.LogoutEndpoint = fmt.Sprintf("%s/logout", server.Issuer)
// // The redirect path when logging out of the OpenShift OAuth Server. Note: this has to be a relative link to the OAuth server
// metadata.LogoutRedirect = fmt.Sprintf("/oauth/authorize?client_id=%s&redirect_uri=%s&response_type=%s", clientId, url.QueryEscape(redirectURL), "token")
// // The fully qualified endpoint to use logging into the OpenShift OAuth server.
// metadata.AuthorizationEndpoint = fmt.Sprintf("%s%s", server.Issuer, metadata.LogoutRedirect)

func (in *OpenshiftOAuthService) buildRequest(ctx context.Context, method string, url string, auth *string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, strings.Join([]string{in.conf.Auth.OpenShift.ServerPrefix, url}, ""), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for api endpoint [%s] for oauth consumption, error: %s", url, err)
	}

	if auth != nil {
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *auth))
	}

	return request, nil
}

// TODO: Move this?
func (in *OpenshiftOAuthService) GetOAuthAuthorizationServer(ctx context.Context) (*OAuthAuthorizationServer, error) {
	var server *OAuthAuthorizationServer

	request, err := in.buildRequest(ctx, "GET", ".well-known/oauth-authorization-server", nil)
	if err != nil {
		log.Error(err)
		message := fmt.Errorf("could not get OAuthAuthorizationServer: %v", err)
		return nil, message
	}

	response, err := doRequest(in.httpclient, request)
	if err != nil {
		log.Error(err)
		message := fmt.Errorf("could not get OAuthAuthorizationServer: %v", err)
		return nil, message
	}

	err = json.Unmarshal(response, &server)
	if err != nil {
		log.Error(err)
		message := fmt.Errorf("could not parse OAuthAuthorizationServer: %v", err)
		return nil, message
	}

	return server, nil
}

func (in *OpenshiftOAuthService) GetUserInfo(ctx context.Context, token string) (*OAuthUser, error) {
	var user *OAuthUser

	request, err := in.buildRequest(ctx, "GET", "apis/user.openshift.io/v1/users/~", &token)
	if err != nil {
		log.Error(err)
		return nil, fmt.Errorf("could not get user info from Openshift: %v", err)
	}

	response, err := doRequest(in.httpclient, request)
	if err != nil {
		log.Error(err)
		return nil, fmt.Errorf("could not get user info from Openshift: %v", err)
	}

	err = json.Unmarshal(response, &user)
	if err != nil {
		log.Error(err)
		return nil, fmt.Errorf("could not parse user info from Openshift: %v", err)
	}

	return user, nil
}

func (in *OpenshiftOAuthService) Logout(ctx context.Context, token string) error {
	// https://github.com/kiali/kiali/issues/3595
	// OpenShift 4.6+ changed the format of the OAuthAccessToken.
	// In pre-4.6, the access_token given to the client is the same name as the OAuthAccessToken resource.
	// In 4.6+, that is not true anymore - you have to encode the access_token to obtain the OAuthAccessToken resource name.
	// The code below will attempt to delete the access token using the new 4.6+ format.

	// convert the access token to the corresponding oauthaccesstoken resource name
	// see: https://github.com/openshift/console/blob/9f352ba49f82ad693a72d0d35709961428b43b93/pkg/server/server.go#L609-L613
	sha256Prefix := "sha256~"
	h := sha256.Sum256([]byte(strings.TrimPrefix(token, sha256Prefix)))
	oauthTokenName := sha256Prefix + base64.RawURLEncoding.EncodeToString(h[0:])
	log.Debugf("Logging out by deleting OAuth access token [%v] which was converted from access token [%v]", oauthTokenName, token)

	// Delete the access token from the API server using OpenShift 4.6+ access token name
	adminToken := in.kialiSAClient.GetToken()
	req, err := in.buildRequest(ctx, "DELETE", fmt.Sprintf("apis/oauth.openshift.io/v1/oauthaccesstokens/%v", oauthTokenName), &adminToken)
	if err != nil {
		return err
	}

	_, err = doRequest(in.httpclient, req)
	if err != nil {
		return err
	}

	return nil
}

func doRequest(client *http.Client, request *http.Request) ([]byte, error) {
	defer client.CloseIdleConnections()

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to get response for api endpoint [%s] for oauth consumption, error: %s", request.URL, err)
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body for api endpoint [%s] for oauth consumption, error: %s", request.URL, err)
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get OK status from api endpoint [%s] for oauth consumption, error: %s", request.URL, string(body))
	}

	return body, nil
}

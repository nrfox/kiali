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

	oauth_v1 "github.com/openshift/api/oauth/v1"
	user_v1 "github.com/openshift/api/user/v1"

	"github.com/kiali/kiali/config"
	"github.com/kiali/kiali/kubernetes"
	"github.com/kiali/kiali/log"
)

const (
	defaultAuthRequestTimeout = 10 * time.Second
	kubeCAFilePath            = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	serverPrefix              = "https://kubernetes.default.svc/"
)

func OpenshiftAuthCACertPool(conf *config.Config) (*x509.CertPool, error) {
	certPool := x509.NewCertPool()

	// Add the kube CA
	if err := readFileAndAppendToCertPool(certPool, kubeCAFilePath); err != nil {
		return nil, err
	}

	// Add custom CA(s)
	if customCAFile := conf.Auth.OpenShift.CAFile; customCAFile != "" {
		log.Debugf("adding custom CA bundle for Openshift OAuth [%v]", customCAFile)
		if err := readFileAndAppendToCertPool(certPool, customCAFile); err != nil {
			return nil, err
		}
	}

	return certPool, nil
}

func readFileAndAppendToCertPool(certPool *x509.CertPool, file string) error {
	cert, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("failed to read CA file '%s': %s", file, err)
	}
	if !certPool.AppendCertsFromPEM(cert) {
		return fmt.Errorf("failed to add custom CA certificates: %s", err)
	}
	return nil
}

func NewOpenshiftOAuthService(conf *config.Config, kialiSAClient kubernetes.ClientInterface) (*OpenshiftOAuthService, error) {
	certPool, err := OpenshiftAuthCACertPool(conf)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{RootCAs: certPool}
	client := &http.Client{
		Timeout: defaultAuthRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
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

func (in *OpenshiftOAuthService) buildRequest(ctx context.Context, method string, url string, auth *string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, strings.Join([]string{serverPrefix, url}, ""), nil)
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

func (in *OpenshiftOAuthService) GetUserInfo(ctx context.Context, token string) (*user_v1.User, error) {
	var user *user_v1.User

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

func (in *OpenshiftOAuthService) GetOAuthClient(ctx context.Context) (*oauth_v1.OAuthClient, error) {
	// Get the OAuthClient for Kiali. This is created by the operator or the helm chart.
	var (
		adminToken       = in.kialiSAClient.GetToken()
		kialiOAuthClient = in.conf.Deployment.InstanceName + "-" + in.conf.Deployment.Namespace
		url              = fmt.Sprintf("apis/oauth.openshift.io/v1/oauthclients/%s", kialiOAuthClient)
	)
	// TODO: Import openshift go client rather than building this request manually.
	request, err := in.buildRequest(ctx, "GET", url, &adminToken)
	if err != nil {
		return nil, err
	}

	response, err := doRequest(in.httpclient, request)
	if err != nil {
		return nil, err
	}

	var oauthClient *oauth_v1.OAuthClient
	err = json.Unmarshal(response, &oauthClient)
	if err != nil {
		return nil, fmt.Errorf("could not parse OAuthClient: %v", err)
	}

	return oauthClient, nil
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

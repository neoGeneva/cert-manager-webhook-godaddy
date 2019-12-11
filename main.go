package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"net/http"
	"bytes"
	"io"
	"io/ioutil"
	"time"

	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/jetstack/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/jetstack/cert-manager/pkg/acme/webhook/cmd"
	certmanager_v1alpha1 "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha1"
	"github.com/jetstack/cert-manager/pkg/issuer/acme/dns/util"
	pkgutil "github.com/jetstack/cert-manager/pkg/util"
)

const (
	defaultTTL = 600
	defaultBaseURL = "https://api.godaddy.com"
)

// GroupName the API is in within Kubernetes, e.g. certmanager.k8s.io
var GroupName = os.Getenv("GROUP_NAME")

func main() {
	if GroupName == "" {
		panic("GROUP_NAME must be specified")
	}

	// This will register our custom DNS provider with the webhook serving
	// library, making it available as an API under the provided GroupName.
	// You can register multiple DNS provider implementations with a single
	// webhook, where the Name() method will be used to disambiguate between
	// the different implementations.
	cmd.RunWebhookServer(GroupName,
		&customDNSProviderSolver{},
	)
}

// customDNSProviderSolver implements the provider-specific logic needed to
// 'present' an ACME challenge TXT record for your own DNS provider.
// To do so, it must implement the `github.com/jetstack/cert-manager/pkg/acme/webhook.Solver`
// interface.
type customDNSProviderSolver struct {
	client *kubernetes.Clientset
}

// customDNSProviderConfig is a structure that is used to decode into when
// solving a DNS01 challenge.
// This information is provided by cert-manager, and may be a reference to
// additional configuration that's needed to solve the challenge for this
// particular certificate or issuer.
// This typically includes references to Secret resources containing DNS
// provider credentials, in cases where a 'multi-tenant' DNS solver is being
// created.
// If you do *not* require per-issuer or per-certificate configuration to be
// provided to your webhook, you can skip decoding altogether in favour of
// using CLI flags or similar to provide configuration.
// You should not include sensitive information here. If credentials need to
// be used by your provider here, you should reference a Kubernetes Secret
// resource and fetch these credentials using a Kubernetes clientset.
type customDNSProviderConfig struct {
	AuthAPIKey        string                                 `json:"authAPIKey"`
	APITokenSecretRef certmanager_v1alpha1.SecretKeySelector `json:"authAPISecretRef"`
	TTL               *int                                   `json:"ttl"`
	apiToken          string;
}

// DNSRecord a DNS record
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Data     string `json:"data"`
	Priority int    `json:"priority,omitempty"`
	TTL      int    `json:"ttl,omitempty"`
}

func (c *customDNSProviderSolver) Name() string {
	return "godaddy"
}

// Present is responsible for actually presenting the DNS record with the
// DNS provider.
// This method should tolerate being called multiple times with the same value.
// cert-manager itself will later perform a self check to ensure that the
// solver has correctly configured the DNS provider.
func (c *customDNSProviderSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}
		
	ref := cfg.APITokenSecretRef

	secret, err := c.client.CoreV1().Secrets(ch.ResourceNamespace).Get(ref.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	apiToken, ok := secret.Data[ref.Key]
	if !ok {
		return fmt.Errorf("no api token for %q in secret '%s/%s'", ref.Name, ref.Key, ch.ResourceNamespace)
	}

	cfg.apiToken = string(apiToken);
	
	name := extractRecordName(ch.ResolvedFQDN, ch.ResolvedZone)
	domain := ch.ResolvedZone[:len(ch.ResolvedZone)-1]

	rec := &DNSRecord{
		Type: "TXT",
		Name: name,
		Data: ch.Key,
		TTL:  *cfg.TTL,
	}

	err = c.updateRecords(rec, domain, cfg)
	if err != nil {
		return err
	}

	return nil
}

// CleanUp should delete the relevant TXT record from the DNS provider console.
// If multiple TXT records exist with the same record name (e.g.
// _acme-challenge.example.com) then **only** the record with the same `key`
// value provided on the ChallengeRequest should be cleaned up.
// This is in order to facilitate multiple DNS validations for the same domain
// concurrently.
func (c *customDNSProviderSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}
		
	ref := cfg.APITokenSecretRef

	secret, err := c.client.CoreV1().Secrets(ch.ResourceNamespace).Get(ref.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	apiToken, ok := secret.Data[ref.Key]
	if !ok {
		return fmt.Errorf("no api token for %q in secret '%s/%s'", ref.Name, ref.Key, ch.ResourceNamespace)
	}

	cfg.apiToken = string(apiToken);

	name := extractRecordName(ch.ResolvedFQDN, ch.ResolvedZone)
	domain := ch.ResolvedZone[:len(ch.ResolvedZone)-1]

	rec := &DNSRecord{
		Type: "TXT",
		Name: name,
		Data: "null",
	}
	
	return c.updateRecords(rec, domain, cfg)
}

// Initialize will be called when the webhook first starts.
// This method can be used to instantiate the webhook, i.e. initialising
// connections or warming up caches.
// Typically, the kubeClientConfig parameter is used to build a Kubernetes
// client that can be used to fetch resources from the Kubernetes API, e.g.
// Secret resources containing credentials used to authenticate with DNS
// provider accounts.
// The stopCh can be used to handle early termination of the webhook, in cases
// where a SIGTERM or similar signal is sent to the webhook process.
func (c *customDNSProviderSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}
	c.client = cl

	return nil
}

// loadConfig is a small helper function that decodes JSON configuration into
// the typed config struct.
func loadConfig(cfgJSON *extapi.JSON) (customDNSProviderConfig, error) {
	ttl := defaultTTL
	cfg := customDNSProviderConfig{TTL: &ttl}
	// handle the 'base case' where no configuration has been provided
	if cfgJSON == nil {
		return cfg, nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("error decoding solver config: %v", err)
	}

	return cfg, nil
}

func extractRecordName(fqdn, zone string) string {
	if idx := strings.Index(fqdn, "."+zone); idx != -1 {
		return fqdn[:idx]
	}

	return util.UnFqdn(fqdn)
}

func (c *customDNSProviderSolver) updateRecords(r *DNSRecord, domainZone string, cfg customDNSProviderConfig) error {
	body, err := json.Marshal([]DNSRecord{*r})
	if err != nil {
		return err
	}

	var resp *http.Response
	resp, err = c.makeRequest(http.MethodPut, fmt.Sprintf("/v1/domains/%s/records/TXT/%s", domainZone, r.Name), bytes.NewReader(body), cfg)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("could not create record %v; Status: %v; Body: %s", string(body), resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func (c *customDNSProviderSolver) makeRequest(method, uri string, body io.Reader, cfg customDNSProviderConfig) (*http.Response, error) {
	req, err := http.NewRequest(method, fmt.Sprintf("%s%s", defaultBaseURL, uri), body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", pkgutil.CertManagerUserAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("sso-key %s:%s", cfg.AuthAPIKey, cfg.apiToken))

	client := http.Client{
		Timeout:   30 * time.Second,
	}

	return client.Do(req)
}

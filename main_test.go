package main

import (
	"os"
	"testing"
	"os/exec"
	"fmt"

	"github.com/jetstack/cert-manager/test/acme/dns"

	"k8s.io/client-go/rest"
)

var (
	zone               = os.Getenv("TEST_ZONE_NAME")
	kubeBuilderBinPath = "./_out/kubebuilder/bin"
	testPath           = "./testdata/my-custom-solver"
)

type testCustomDNSProviderSolver struct {
	customDNSProviderSolver
}

func (c *testCustomDNSProviderSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	err := c.customDNSProviderSolver.Initialize(kubeClientConfig, stopCh);

	if err != nil {
		return err
	}

	cmd := exec.Command(
		fmt.Sprintf("%s/kubectl", kubeBuilderBinPath),
		"apply",
		"-f",
		fmt.Sprintf("%s/api-key.yaml", testPath),
		"-s",
		kubeClientConfig.Host,
	)

	_, err = cmd.Output()

	return err
}

func TestRunsSuite(t *testing.T) {
	// The manifest path should contain a file named config.json that is a
	// snippet of valid configuration that should be included on the
	// ChallengeRequest passed as part of the test cases.

	fixture := dns.NewFixture(&testCustomDNSProviderSolver{},
		dns.SetBinariesPath(kubeBuilderBinPath),
		dns.SetResolvedZone(zone),
		dns.SetAllowAmbientCredentials(false),
		dns.SetManifestPath(testPath),
	)

	fixture.RunConformance(t)

}

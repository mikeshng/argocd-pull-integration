//go:build e2e
// +build e2e

/*
Copyright 2025 Open Cluster Management.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"open-cluster-management.io/argocd-pull-integration/test/utils"
)

// Autonomous mode: the workload (spoke) cluster owns its Application configuration directly.
// The agent transmits creation/changes to the principal on the hub, which mirrors them
// read-only into a per-managed-cluster namespace (named after the ManagedCluster, e.g.
// "cluster1") for observability. Unlike managed mode, there is no argocd-server on the spoke
// and no hub-to-spoke configuration push, so an AppProject must already exist locally (in a
// real deployment this is expected to be provisioned via GitOps/app-of-apps, per upstream
// argocd-agent's autonomous mode docs: "Argo CD configuration management must be
// externalized"). This test provisions that AppProject directly to stand in for that GitOps
// step.
var _ = Describe("Advanced Pull Model E2E - Autonomous Mode", Label("advanced-pull-autonomous"), Ordered, func() {
	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(5 * time.Second)

	const (
		hubMirrorNamespace = "cluster1" // principal mirrors autonomous agent Applications into a namespace named after the ManagedCluster
		appName            = "autonomous-guestbook"
		targetNamespace    = "guestbook-autonomous"
	)

	BeforeAll(func() {
		By("Verifying test environment is ready")
		cmd := exec.Command("kubectl", "config", "get-contexts", hubContext)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Hub context should exist - run 'make test-e2e-advanced-pull-autonomous-local' to set up")

		cmd = exec.Command("kubectl", "config", "get-contexts", cluster1Context)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Spoke context should exist - run 'make test-e2e-advanced-pull-autonomous-local' to set up")
	})

	AfterAll(func() {
		By("Test complete - clusters preserved for inspection")
		fmt.Fprintf(GinkgoWriter, "\n")
		fmt.Fprintf(GinkgoWriter, "Clusters have been preserved for inspection:\n")
		fmt.Fprintf(GinkgoWriter, "  Hub: kubectl config use-context kind-hub\n")
		fmt.Fprintf(GinkgoWriter, "  Spoke: kubectl config use-context kind-cluster1\n")
		fmt.Fprintf(GinkgoWriter, "\n")
	})

	Context("Autonomous Addon Deployment", func() {
		It("should deliver a spoke ArgoCD CR configured for autonomous mode", func() {
			By("verifying GitOpsCluster reports the ArgoCD CR was delivered")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "gitopscluster", "gitops-cluster",
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ArgoCDCRDelivered')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}).Should(Succeed())

			By("verifying the spoke ArgoCD CR client mode is autonomous")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "argocd", "argocd",
					"-n", argoCDNamespace,
					"-o", "jsonpath={.spec.argoCDAgent.agent.client.mode}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("autonomous"))
			}).Should(Succeed())

			By("verifying destinationBasedMapping is not set (argocd-agent rejects it for autonomous agents)")
			cmd := exec.Command("kubectl", "--context", cluster1Context,
				"get", "argocd", "argocd",
				"-n", argoCDNamespace,
				"-o", "jsonpath={.spec.argoCDAgent.agent.destinationBasedMapping}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(BeEmpty())
		})

		It("should run the ArgoCD agent connected to the principal", func() {
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "pods", "-n", argoCDNamespace,
					"-l", "app.kubernetes.io/name=argocd-agent-agent",
					"-o", "jsonpath={.items[0].status.phase}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"))
			}).Should(Succeed())
		})
	})

	Context("Application created directly on the spoke cluster", func() {
		It("should accept an Application created directly on the spoke (no hub involvement)", func() {
			By("provisioning the default AppProject on the spoke (stand-in for GitOps/app-of-apps bootstrap)")
			appProjectYAML := `apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: default
  namespace: argocd
spec:
  sourceRepos:
  - '*'
  destinations:
  - namespace: '*'
    server: '*'
  clusterResourceWhitelist:
  - group: '*'
    kind: '*'`
			cmd := exec.Command("kubectl", "--context", cluster1Context, "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(appProjectYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating an Application directly on the spoke cluster")
			appYAML := fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: %s
  namespace: argocd
spec:
  project: default
  source:
    repoURL: https://github.com/argoproj/argocd-example-apps.git
    targetRevision: HEAD
    path: guestbook
  destination:
    server: https://kubernetes.default.svc
    namespace: %s
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
    - CreateNamespace=true`, appName, targetNamespace)
			cmd = exec.Command("kubectl", "--context", cluster1Context, "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(appYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should transmit the Application to the hub principal, read-only, in a per-cluster namespace", func() {
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "application", appName,
					"-n", hubMirrorNamespace,
					"-o", "jsonpath={.metadata.name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal(appName))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("should sync the Application on the spoke and reflect status back to the hub", func() {
			By("verifying Application sync status on spoke")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "application", appName,
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.sync.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Synced"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying Application health status on spoke")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "application", appName,
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.health.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Healthy"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying guestbook deployment is running on spoke")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "deployment", "guestbook-ui",
					"-n", targetNamespace,
					"-o", "jsonpath={.status.availableReplicas}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("1"))
			}).Should(Succeed())

			By("verifying Application status mirrored to the hub matches the spoke")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "application", appName,
					"-n", hubMirrorNamespace,
					"-o", "jsonpath={.status.sync.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Synced"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "application", appName,
					"-n", hubMirrorNamespace,
					"-o", "jsonpath={.status.health.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Healthy"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "Application %s created directly on spoke, synced, and mirrored to hub in namespace %s\n", appName, hubMirrorNamespace)
		})
	})
})

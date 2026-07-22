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

// This suite exercises hubArgoCD.enabled=false: adopting a pre-existing argocd-operator-managed
// Argo CD instance on the hub instead of letting the chart install its own. The Makefile target
// simulates "pre-existing" by applying the chart's own hub argocd-operator/ArgoCD CR templates
// directly via kubectl before installing the chart, standing in for an install done by other
// means (a separate Helm release, a different tool, or a hand-rolled manifest).
var _ = Describe("Advanced Pull Model E2E - Existing Hub ArgoCD", Label("advanced-pull-existing-argocd"), Ordered, func() {
	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(5 * time.Second)

	const (
		appSetName      = "test-appset"
		targetNamespace = "guestbook"
	)

	BeforeAll(func() {
		By("Verifying test environment is ready")
		cmd := exec.Command("kubectl", "config", "get-contexts", hubContext)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Hub context should exist - run 'make test-e2e-advanced-pull-existing-argocd-local' to set up")

		cmd = exec.Command("kubectl", "config", "get-contexts", cluster1Context)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Spoke context should exist - run 'make test-e2e-advanced-pull-existing-argocd-local' to set up")
	})

	AfterAll(func() {
		By("Test complete - clusters preserved for inspection")
		fmt.Fprintf(GinkgoWriter, "\n")
		fmt.Fprintf(GinkgoWriter, "Clusters have been preserved for inspection:\n")
		fmt.Fprintf(GinkgoWriter, "  Hub: kubectl config use-context kind-hub\n")
		fmt.Fprintf(GinkgoWriter, "  Spoke: kubectl config use-context kind-cluster1\n")
		fmt.Fprintf(GinkgoWriter, "\n")
	})

	Context("Chart installed with hubArgoCD.enabled=false", func() {
		It("should not render any hub argocd-operator or ArgoCD CR resources", func() {
			cmd := exec.Command("helm", "get", "manifest", "argocd-agent-addon",
				"--namespace", argoCDNamespace, "--kube-context", hubContext)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(ContainSubstring("kind: ArgoCD\n"), "hubArgoCD.enabled=false must not render a hub ArgoCD CR")
			Expect(output).NotTo(ContainSubstring("name: argocd-operator-controller-manager"), "hubArgoCD.enabled=false must not render the hub argocd-operator Deployment")
		})

		It("should reuse the pre-existing argocd-operator and principal instead of installing a second one", func() {
			By("verifying exactly one argocd-operator controller-manager Deployment exists")
			cmd := exec.Command("kubectl", "--context", hubContext,
				"get", "deployment", "argocd-operator-controller-manager",
				"-n", operatorNamespace,
				"-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(output)).To(Equal("1"))

			By("verifying the principal pod from the pre-existing install is running")
			cmd = exec.Command("kubectl", "--context", hubContext,
				"get", "pods", "-n", argoCDNamespace,
				"-l", "app.kubernetes.io/name=argocd-agent-principal",
				"-o", "jsonpath={.items[0].status.phase}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("Running"))
		})

		It("should still deploy the GitOpsCluster controller and reconcile successfully", func() {
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "gitopscluster", "gitops-cluster",
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='AddonConfigured')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			cmd := exec.Command("kubectl", "--context", hubContext,
				"get", "managedclusteraddon", "argocd-agent-addon", "-n", "cluster1")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should deploy the ArgoCD agent on the spoke cluster", func() {
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

	Context("Application Sync against the adopted hub ArgoCD", func() {
		var appName string

		It("should sync AppProject from hub to spoke", func() {
			appProjectYAML := `apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: default
  namespace: argocd
  labels:
    e2e-sync-test: "true"
spec:
  clusterResourceWhitelist:
  - group: '*'
    kind: '*'
  destinations:
  - namespace: '*'
    server: '*'
  sourceRepos:
  - '*'
  sourceNamespaces:
  - '*'`
			cmd := exec.Command("kubectl", "--context", hubContext, "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(appProjectYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "appproject", "default",
					"-n", argoCDNamespace,
					"-o", "jsonpath={.metadata.labels.e2e-sync-test}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("true"))
			}, 30*time.Second).Should(Succeed())
		})

		It("should create Application from ApplicationSet and sync it to the spoke", func() {
			appSetYAML := fmt.Sprintf(`apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: %s
  namespace: argocd
spec:
  generators:
  - clusterDecisionResource:
      configMapRef: ocm-placement-generator
      labelSelector:
        matchLabels:
          cluster.open-cluster-management.io/placement: placement
      requeueAfterSeconds: 30
  template:
    metadata:
      name: '{{name}}-app'
    spec:
      project: default
      source:
        repoURL: https://github.com/argoproj/argocd-example-apps.git
        targetRevision: HEAD
        path: guestbook
      destination:
        name: '{{name}}'
        namespace: %s
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
        - CreateNamespace=true`, appSetName, targetNamespace)
			cmd := exec.Command("kubectl", "--context", hubContext, "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(appSetYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			appName = "cluster1-app"
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "application", appName, "-n", argoCDNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}).Should(Succeed())

			By("verifying Application syncs to spoke and reports healthy status on both sides")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", cluster1Context,
					"get", "application", appName,
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.sync.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Synced"))
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "--context", hubContext,
					"get", "application", appName,
					"-n", argoCDNamespace,
					"-o", "jsonpath={.status.sync.status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Synced"))
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			fmt.Fprintf(GinkgoWriter, "Application %s synced to spoke against the adopted (pre-existing) hub ArgoCD\n", appName)
		})
	})
})

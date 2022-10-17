/*
Copyright 2022.

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

package application

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

var _ = Describe("Foo controller", func() {

	const (
		appName      = "app-1"
		appNamespace = "default"
	)

	Context("When Application without OCM pull label is created", func() {
		It("Should not create ManifestWork", func() {
			By("Creating the Application without OCM pull label")
			ctx := context.Background()
			app1 := argov1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      appName,
					Namespace: appNamespace,
				},
			}
			Expect(k8sClient.Create(ctx, &app1)).Should(Succeed())
		})
	})
})

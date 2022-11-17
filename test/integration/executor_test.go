package integration

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	workapiv1 "open-cluster-management.io/api/work/v1"
	"open-cluster-management.io/work/pkg/spoke"
	"open-cluster-management.io/work/test/integration/util"
)

var _ = ginkgo.Describe("ManifestWork Executor Subject", func() {
	var o *spoke.WorkloadAgentOptions
	var cancel context.CancelFunc

	var work *workapiv1.ManifestWork
	var manifests []workapiv1.Manifest
	var executor *workapiv1.ManifestWorkExecutor

	var err error

	ginkgo.BeforeEach(func() {
		o = spoke.NewWorkloadAgentOptions()
		o.HubKubeconfigFile = hubKubeconfigFileName
		o.SpokeClusterName = utilrand.String(5)
		o.StatusSyncInterval = 3 * time.Second

		ns := &corev1.Namespace{}
		ns.Name = o.SpokeClusterName
		_, err := spokeKubeClient.CoreV1().Namespaces().Create(context.Background(), ns, metav1.CreateOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		go startWorkAgent(ctx, o)

		// reset manifests
		manifests = nil
		executor = nil
	})

	ginkgo.JustBeforeEach(func() {
		work = util.NewManifestWork(o.SpokeClusterName, "", manifests)
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
		work.Spec.Executor = executor
	})

	ginkgo.AfterEach(func() {
		if cancel != nil {
			cancel()
		}
		err := spokeKubeClient.CoreV1().Namespaces().Delete(
			context.Background(), o.SpokeClusterName, metav1.DeleteOptions{})
		gomega.Expect(err).ToNot(gomega.HaveOccurred())
	})

	ginkgo.Context("Apply the resource with executor", func() {
		executorName := "test-executor"
		ginkgo.BeforeEach(func() {
			manifests = []workapiv1.Manifest{
				util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm2", map[string]string{"c": "d"}, []string{})),
			}
			executor = &workapiv1.ManifestWorkExecutor{
				Subject: workapiv1.ManifestWorkExecutorSubject{
					Type: workapiv1.ExecutorSubjectTypeServiceAccount,
					ServiceAccount: &workapiv1.ManifestWorkSubjectServiceAccount{
						Namespace: o.SpokeClusterName,
						Name:      executorName,
					},
				},
			}
		})

		ginkgo.It("Executor does not have permission", func() {
			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps not exist
			util.AssertNonexistenceOfConfigMaps(manifests, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("Executor does not have permission to partial resources", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"cm1"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmap cm1 exist and cm2 not exist
			util.AssertExistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
			util.AssertNonexistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm2", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("Executor has permission for all resources", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"cm1", "cm2"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionTrue, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionTrue, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps all exist
			util.AssertExistenceOfConfigMaps(manifests, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})
	})

	ginkgo.Context("Apply the resource with executor deleting validating", func() {
		executorName := "test-executor"
		ginkgo.BeforeEach(func() {
			manifests = []workapiv1.Manifest{
				util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm2", map[string]string{"c": "d"}, []string{})),
			}
			executor = &workapiv1.ManifestWorkExecutor{
				Subject: workapiv1.ManifestWorkExecutorSubject{
					Type: workapiv1.ExecutorSubjectTypeServiceAccount,
					ServiceAccount: &workapiv1.ManifestWorkSubjectServiceAccount{
						Namespace: o.SpokeClusterName,
						Name:      executorName,
					},
				},
			}
		})

		ginkgo.It("Executor does not have delete permission and delete option is foreground", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"create", "update", "patch", "get", "list"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"cm1", "cm2"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps not exist
			util.AssertNonexistenceOfConfigMaps(manifests, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("Executor does not have delete permission and delete option is  orphan", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"create", "update", "patch", "get", "list"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"cm1", "cm2"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work.Spec.DeleteOption = &workapiv1.DeleteOption{
				PropagationPolicy: workapiv1.DeletePropagationPolicyTypeOrphan,
			}
			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionTrue, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionTrue, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps all exist
			util.AssertExistenceOfConfigMaps(manifests, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("Executor does not have delete permission and delete option is  selectively orphan", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:         []string{"create", "update", "patch", "get", "list"},
							APIGroups:     []string{""},
							Resources:     []string{"configmaps"},
							ResourceNames: []string{"cm1", "cm2"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: o.SpokeClusterName,
						Name:      roleName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work.Spec.DeleteOption = &workapiv1.DeleteOption{
				PropagationPolicy: workapiv1.DeletePropagationPolicyTypeSelectivelyOrphan,
				SelectivelyOrphan: &workapiv1.SelectivelyOrphan{
					OrphaningRules: []workapiv1.OrphaningRule{
						{
							Resource:  "configmaps",
							Namespace: o.SpokeClusterName,
							Name:      "cm1",
						},
					},
				},
			}
			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse, []metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmap cm1 exist and cm2 not exist
			util.AssertExistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
			util.AssertNonexistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm2", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})
	})

	ginkgo.Context("Apply the resource with executor escalation validating", func() {
		executorName := "test-executor"
		ginkgo.BeforeEach(func() {
			manifests = []workapiv1.Manifest{
				util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				util.ToManifest(util.NewRoleForManifest(o.SpokeClusterName, "role-cm-creator", rbacv1.PolicyRule{
					Verbs:     []string{"create", "update", "patch", "get", "list", "delete"},
					APIGroups: []string{""},
					Resources: []string{"configmaps"},
				})),
				util.ToManifest(util.NewRoleBindingForManifest(o.SpokeClusterName, "role-cm-creator-binding",
					rbacv1.RoleRef{
						Kind: "Role",
						Name: "role-cm-creator",
					},
					rbacv1.Subject{
						Kind:      "ServiceAccount",
						Namespace: o.SpokeClusterName,
						Name:      executorName,
					})),
			}
			executor = &workapiv1.ManifestWorkExecutor{
				Subject: workapiv1.ManifestWorkExecutorSubject{
					Type: workapiv1.ExecutorSubjectTypeServiceAccount,
					ServiceAccount: &workapiv1.ManifestWorkSubjectServiceAccount{
						Namespace: o.SpokeClusterName,
						Name:      executorName,
					},
				},
			}
		})

		ginkgo.It("no permission", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							// no "escalate" and "bind" verb
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"roles", "rolebindings"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse,
				[]metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse,
				[]metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmap not exist
			util.AssertNonexistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("no permission for already exist resource", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							// no "escalate" and "bind" verb
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"roles", "rolebindings"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// make the role exist with lower permission
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "role-cm-creator",
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{"get", "list"},
							APIGroups: []string{""},
							Resources: []string{"configmaps"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionFalse,
				[]metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionFalse, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionFalse,
				// the cluster role already esists, so the ailable status is true enen if the applied status is false
				[]metav1.ConditionStatus{metav1.ConditionFalse, metav1.ConditionTrue, metav1.ConditionFalse},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmap not exist
			util.AssertNonexistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("with permission", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							// with "escalate" and "bind" verb
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete", "escalate", "bind"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"roles"},
						},
						{
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"rolebindings"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionTrue,
				[]metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout*3, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionTrue,
				[]metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps exist
			util.AssertExistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})

		ginkgo.It("with permission for already exist resource", func() {
			roleName := "role1"
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							// with "escalate" and "bind" verb
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete", "escalate", "bind"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"roles"},
						},
						{
							Verbs:     []string{"create", "update", "patch", "get", "list", "delete"},
							APIGroups: []string{"rbac.authorization.k8s.io"},
							Resources: []string{"rolebindings"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())
			_, err = spokeKubeClient.RbacV1().RoleBindings(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.RoleBinding{
					ObjectMeta: metav1.ObjectMeta{
						Name:      roleName,
						Namespace: o.SpokeClusterName,
					},
					Subjects: []rbacv1.Subject{
						{
							Kind:      "ServiceAccount",
							Namespace: o.SpokeClusterName,
							Name:      executorName,
						},
					},
					RoleRef: rbacv1.RoleRef{
						APIGroup: "rbac.authorization.k8s.io",
						Kind:     "Role",
						Name:     roleName,
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			// make the role exist with lower permission
			_, err = spokeKubeClient.RbacV1().Roles(o.SpokeClusterName).Create(
				context.TODO(), &rbacv1.Role{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "role-cm-creator",
						Namespace: o.SpokeClusterName,
					},
					Rules: []rbacv1.PolicyRule{
						{
							Verbs:     []string{"get", "list"},
							APIGroups: []string{""},
							Resources: []string{"configmaps"},
						},
					},
				}, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			work, err = hubWorkClient.WorkV1().ManifestWorks(o.SpokeClusterName).Create(
				context.Background(), work, metav1.CreateOptions{})
			gomega.Expect(err).ToNot(gomega.HaveOccurred())

			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkApplied),
				metav1.ConditionTrue,
				[]metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout*3, eventuallyInterval)
			util.AssertWorkCondition(work.Namespace, work.Name, hubWorkClient, string(workapiv1.WorkAvailable),
				metav1.ConditionTrue,
				[]metav1.ConditionStatus{metav1.ConditionTrue, metav1.ConditionTrue, metav1.ConditionTrue},
				eventuallyTimeout, eventuallyInterval)

			// ensure configmaps exist
			util.AssertExistenceOfConfigMaps(
				[]workapiv1.Manifest{
					util.ToManifest(util.NewConfigmap(o.SpokeClusterName, "cm1", map[string]string{"a": "b"}, []string{})),
				}, spokeKubeClient, eventuallyTimeout, eventuallyInterval)
		})
	})
})

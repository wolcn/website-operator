// internal/controller/website_controller.go

package controller

import (
	"context"
	"reflect"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	intstr "k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	websitev1alpha1 "github.com/wolcn/website-operator/api/v1alpha1"
)

// WebsiteReconciler reconciles a Website object
type WebsiteReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=website.my.domain,resources=websites,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=website.my.domain,resources=websites/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=website.my.domain,resources=websites/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *WebsiteReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1. Fetch the Website instance
	website := &websitev1alpha1.Website{}
	err := r.Get(ctx, req.NamespacedName, website)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Website resource not found. Ignoring since object must be deleted.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get Website")
		return ctrl.Result{}, err
	}

	// 2. Reconcile the Deployment
	deployment := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: website.Name, Namespace: website.Namespace}, deployment)

	// If the deployment does not exist, create it. Otherwise, update it.
	if err != nil && errors.IsNotFound(err) {
		// Define a new deployment
		dep := r.deploymentForWebsite(website)
		logger.Info("Creating a new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		err = r.Create(ctx, dep)
		if err != nil {
			logger.Error(err, "Failed to create new Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}
		// Deployment created successfully - return and requeue
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	}

	// Ensure the deployment size is the same as the spec
	size := website.Spec.Replicas
	if *deployment.Spec.Replicas != *size {
		deployment.Spec.Replicas = size
		err = r.Update(ctx, deployment)
		if err != nil {
			logger.Error(err, "Failed to update Deployment", "Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
			return ctrl.Result{}, err
		}
		// Ask to requeue after 1 minute in order to give time for the
		// pods be created
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// 3. Reconcile the Service
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: website.Name, Namespace: website.Namespace}, service)
	if err != nil && errors.IsNotFound(err) {
		// Define a new service
		svc := r.serviceForWebsite(website)
		logger.Info("Creating a new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		err = r.Create(ctx, svc)
		if err != nil {
			logger.Error(err, "Failed to create new Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		logger.Error(err, "Failed to get Service")
		return ctrl.Result{}, err
	}

	// 4. Update the Website status
	// We'll just reflect the deployment status for simplicity
	if !reflect.DeepEqual(deployment.Status, website.Status.DeploymentStatus) {
		website.Status.DeploymentStatus = deployment.Status
		err := r.Status().Update(ctx, website)
		if err != nil {
			logger.Error(err, "Failed to update Website status")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// deploymentForWebsite returns a Website Deployment object
func (r *WebsiteReconciler) deploymentForWebsite(w *websitev1alpha1.Website) *appsv1.Deployment {
	labels := map[string]string{"app": w.Name}
	replicas := w.Spec.Replicas

	// For a real-world controller, you would likely use an initContainer
	// to clone the gitRepo into a volume. For this tutorial, we'll use
	// a simple nginx image.
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.Name,
			Namespace: w.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image: "nginx:1.14.2",
						Name:  "web-server",
						Ports: []corev1.ContainerPort{{
							ContainerPort: 80,
							Name:          "http",
						}},
					}},
				},
			},
		},
	}

	// Set Website instance as the owner and controller
	ctrl.SetControllerReference(w, dep, r.Scheme)
	return dep
}

// serviceForWebsite returns a Website Service object
func (r *WebsiteReconciler) serviceForWebsite(w *websitev1alpha1.Website) *corev1.Service {
	labels := map[string]string{"app": w.Name}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      w.Name,
			Namespace: w.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Protocol:   corev1.ProtocolTCP,
				Port:       80,
				TargetPort: intstr.FromString("http"),
			}},
			Type: corev1.ServiceTypeLoadBalancer,
		},
	}
	// Set Website instance as the owner and controller
	ctrl.SetControllerReference(w, svc, r.Scheme)
	return svc
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebsiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&websitev1alpha1.Website{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

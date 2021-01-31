/*


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

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	k8serror "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	"multisite/wordpress"
	"time"

	//"multisite/wordpress"

	//appsv1 "k8s.io/api/apps/v1"
	//corev1 "k8s.io/api/core/v1"
	//extionv1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	jcyv1alpha1 "multisite/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MultiSiteReconciler reconciles a MultiSite object
type MultiSiteReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	Config *rest.Config
}

// +kubebuilder:rbac:groups=jcy.ismewen.com,resources=multisites,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=jcy.ismewen.com,resources=multisites/status,verbs=get;update;patch

func (r *MultiSiteReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("multisite", req.NamespacedName)

	// your logic here

	log.Info("welcome new world")
	instance := jcyv1alpha1.MultiSite{}
	if err := r.Get(context.TODO(), req.NamespacedName, &instance); err != nil {
		if k8serror.IsNotFound(err) {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, err
	}

	if !instance.ObjectMeta.DeletionTimestamp.IsZero() {
		// instance 被删除
		log.Info("兽人永不为奴，除非包吃包住")
		wms := NewWordpressMultiSite(instance, r.Config, ctx)
		if err := wms.DeleteSite(); err != nil {
			// 删除失败
			log.Info("Delete failed: %s", err.Error())
			return ctrl.Result{Requeue: true, RequeueAfter: time.Duration(10 * 60)}, err
		} else {
			// 删除成功
			log.Info("Delete success")
			instance.SetFinalizers(removeString(instance.GetFinalizers(), instance.Namespace))
			if err := r.Update(ctx, &instance); err != nil {
				log.Info("Delete update failed")
				return ctrl.Result{}, err
			}
		}
	} else {
		// 被创建 or 被更新
		currentStatus := instance.Spec.Status
		log.Info(fmt.Sprintf("cuurent status:%s", currentStatus))
		if currentStatus == "Init" {
			// 创建多站点
			wms := NewWordpressMultiSite(instance, r.Config, ctx)
			if err := wms.CreateSite(); err != nil {
				// 创建失败
				instance.Spec.Status = "Failed"
				instance.Spec.ErrorMsg = err.Error()
			} else {
				instance.Spec.Status = "Success"
			}
			// 添加 GetFinalizers
			instance.SetFinalizers(append(instance.GetFinalizers(), instance.Namespace))
			if err := r.Update(ctx, &instance); err != nil {
				return ctrl.Result{}, err
			}
		}

	}

	return ctrl.Result{}, nil
}

func (r *MultiSiteReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jcyv1alpha1.MultiSite{}).
		Complete(r)
}

func NewWordpressMultiSite(instance jcyv1alpha1.MultiSite, restConfig *rest.Config, context context.Context) *wordpress.MultiSite {
	ms := wordpress.MultiSite{
		PodName:       instance.Spec.PodName,
		NameSpace:     instance.ObjectMeta.Namespace,
		ContainerName: instance.Spec.ContainerName,
		NickName:      instance.Spec.NickName,
		Ip:            instance.Spec.Ip,
		Config:        restConfig,
		Context:       context,
	}
	return &ms
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

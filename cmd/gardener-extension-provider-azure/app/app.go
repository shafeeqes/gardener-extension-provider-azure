// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"context"
	"fmt"
	"os"

	azureinstall "github.com/gardener/gardener-extension-provider-azure/pkg/apis/azure/install"
	"github.com/gardener/gardener-extension-provider-azure/pkg/azure"
	azurecmd "github.com/gardener/gardener-extension-provider-azure/pkg/cmd"
	azurebackupbucket "github.com/gardener/gardener-extension-provider-azure/pkg/controller/backupbucket"
	azurebackupentry "github.com/gardener/gardener-extension-provider-azure/pkg/controller/backupentry"
	azurecontrolplane "github.com/gardener/gardener-extension-provider-azure/pkg/controller/controlplane"
	azurecsimigration "github.com/gardener/gardener-extension-provider-azure/pkg/controller/csimigration"
	azurednsrecord "github.com/gardener/gardener-extension-provider-azure/pkg/controller/dnsrecord"
	"github.com/gardener/gardener-extension-provider-azure/pkg/controller/healthcheck"
	azureinfrastructure "github.com/gardener/gardener-extension-provider-azure/pkg/controller/infrastructure"
	azureworker "github.com/gardener/gardener-extension-provider-azure/pkg/controller/worker"
	azurecontrolplaneexposure "github.com/gardener/gardener-extension-provider-azure/pkg/webhook/controlplaneexposure"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/v1alpha1"
	azurebastion "github.com/gardener/gardener-extension-provider-azure/pkg/controller/bastion"
	"github.com/gardener/gardener/extensions/pkg/controller"
	controllercmd "github.com/gardener/gardener/extensions/pkg/controller/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/controlplane/genericactuator"
	"github.com/gardener/gardener/extensions/pkg/controller/heartbeat"
	heartbeatcmd "github.com/gardener/gardener/extensions/pkg/controller/heartbeat/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/worker"
	"github.com/gardener/gardener/extensions/pkg/util"
	webhookcmd "github.com/gardener/gardener/extensions/pkg/webhook/cmd"
	gardenerhealthz "github.com/gardener/gardener/pkg/healthz"
	machinev1alpha1 "github.com/gardener/machine-controller-manager/pkg/apis/machine/v1alpha1"
	azurev1alpha1 "github.com/gardener/remedy-controller/pkg/apis/azure/v1alpha1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	autoscalingv1 "k8s.io/autoscaler/vertical-pod-autoscaler/pkg/apis/autoscaling.k8s.io/v1"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/component-base/version/verflag"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// NewControllerManagerCommand creates a new command for running a Azure provider controller.
func NewControllerManagerCommand(ctx context.Context) *cobra.Command {
	var (
		generalOpts = &controllercmd.GeneralOptions{}
		restOpts    = &controllercmd.RESTOptions{}
		mgrOpts     = &controllercmd.ManagerOptions{
			LeaderElection:             true,
			LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
			LeaderElectionID:           controllercmd.LeaderElectionNameID(azure.Name),
			LeaderElectionNamespace:    os.Getenv("LEADER_ELECTION_NAMESPACE"),
			WebhookServerPort:          443,
			WebhookCertDir:             "/tmp/gardener-extensions-cert",
			MetricsBindAddress:         ":8080",
			HealthBindAddress:          ":8081",
		}
		configFileOpts = &azurecmd.ConfigOptions{}

		// options for the backupbucket controller
		backupBucketCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the backupentry controller
		backupEntryCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the bastion controller
		bastionCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the health care controller
		healthCheckCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the heartbeat controller
		heartbeatCtrlOpts = &heartbeatcmd.Options{
			ExtensionName:        azure.Name,
			RenewIntervalSeconds: 30,
			Namespace:            os.Getenv("LEADER_ELECTION_NAMESPACE"),
		}

		// options for the controlplane controller
		controlPlaneCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the csimigration controller
		csiMigrationCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the dnsrecord controller
		dnsRecordCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}

		// options for the infrastructure controller
		infraCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}
		reconcileOpts = &controllercmd.ReconcilerOptions{}

		// options for the worker controller
		workerCtrlOpts = &controllercmd.ControllerOptions{
			MaxConcurrentReconciles: 5,
		}
		workerReconcileOpts = &worker.Options{
			DeployCRDs: true,
		}
		workerCtrlOptsUnprefixed = controllercmd.NewOptionAggregator(workerCtrlOpts, workerReconcileOpts)

		// options for the webhook server
		webhookServerOptions = &webhookcmd.ServerOptions{
			Namespace: os.Getenv("WEBHOOK_CONFIG_NAMESPACE"),
		}

		gardenerVersion    = new(string)
		controllerSwitches = azurecmd.ControllerSwitchOptions()
		webhookSwitches    = azurecmd.WebhookSwitchOptions(gardenerVersion)
		webhookOptions     = webhookcmd.NewAddToManagerOptions(
			azure.Name,
			genericactuator.ShootWebhooksResourceName,
			genericactuator.ShootWebhookNamespaceSelector(azure.Type),
			webhookServerOptions,
			webhookSwitches,
		)

		aggOption = controllercmd.NewOptionAggregator(
			generalOpts,
			restOpts,
			mgrOpts,
			controllercmd.PrefixOption("backupbucket-", backupBucketCtrlOpts),
			controllercmd.PrefixOption("backupentry-", backupEntryCtrlOpts),
			controllercmd.PrefixOption("bastion-", bastionCtrlOpts),
			controllercmd.PrefixOption("controlplane-", controlPlaneCtrlOpts),
			controllercmd.PrefixOption("csimigration-", csiMigrationCtrlOpts),
			controllercmd.PrefixOption("dnsrecord-", dnsRecordCtrlOpts),
			controllercmd.PrefixOption("infrastructure-", infraCtrlOpts),
			controllercmd.PrefixOption("worker-", &workerCtrlOptsUnprefixed),
			controllercmd.PrefixOption("healthcheck-", healthCheckCtrlOpts),
			controllercmd.PrefixOption("heartbeat-", heartbeatCtrlOpts),
			configFileOpts,
			controllerSwitches,
			reconcileOpts,
			webhookOptions,
		)
	)

	cmd := &cobra.Command{
		Use: fmt.Sprintf("%s-controller-manager", azure.Name),

		RunE: func(cmd *cobra.Command, args []string) error {
			verflag.PrintAndExitIfRequested()

			if err := aggOption.Complete(); err != nil {
				return fmt.Errorf("error completing options: %w", err)
			}

			if err := heartbeatCtrlOpts.Validate(); err != nil {
				return err
			}

			util.ApplyClientConnectionConfigurationToRESTConfig(configFileOpts.Completed().Config.ClientConnection, restOpts.Completed().Config)

			if workerReconcileOpts.Completed().DeployCRDs {
				if err := worker.ApplyMachineResourcesForConfig(ctx, restOpts.Completed().Config); err != nil {
					return fmt.Errorf("error ensuring the machine CRDs: %w", err)
				}
			}

			mgr, err := manager.New(restOpts.Completed().Config, mgrOpts.Completed().Options())
			if err != nil {
				return fmt.Errorf("could not instantiate manager: %w", err)
			}

			scheme := mgr.GetScheme()
			if err := controller.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}
			if err := azureinstall.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}
			if err := druidv1alpha1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}
			if err := autoscalingv1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}
			if err := machinev1alpha1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}
			if err := azurev1alpha1.AddToScheme(scheme); err != nil {
				return fmt.Errorf("could not update manager scheme: %w", err)
			}

			// add common meta types to schema for controller-runtime to use v1.ListOptions
			metav1.AddToGroupVersion(scheme, machinev1alpha1.SchemeGroupVersion)

			*gardenerVersion = generalOpts.Completed().GardenerVersion

			configFileOpts.Completed().ApplyETCDStorage(&azurecontrolplaneexposure.DefaultAddOptions.ETCDStorage)
			configFileOpts.Completed().ApplyHealthCheckConfig(&healthcheck.DefaultAddOptions.HealthCheckConfig)
			healthCheckCtrlOpts.Completed().Apply(&healthcheck.DefaultAddOptions.Controller)
			heartbeatCtrlOpts.Completed().Apply(&heartbeat.DefaultAddOptions)
			backupBucketCtrlOpts.Completed().Apply(&azurebackupbucket.DefaultAddOptions.Controller)
			backupEntryCtrlOpts.Completed().Apply(&azurebackupentry.DefaultAddOptions.Controller)
			bastionCtrlOpts.Completed().Apply(&azurebastion.DefaultAddOptions.Controller)
			controlPlaneCtrlOpts.Completed().Apply(&azurecontrolplane.DefaultAddOptions.Controller)
			csiMigrationCtrlOpts.Completed().Apply(&azurecsimigration.DefaultAddOptions.Controller)
			dnsRecordCtrlOpts.Completed().Apply(&azurednsrecord.DefaultAddOptions.Controller)
			infraCtrlOpts.Completed().Apply(&azureinfrastructure.DefaultAddOptions.Controller)
			reconcileOpts.Completed().Apply(&azureinfrastructure.DefaultAddOptions.IgnoreOperationAnnotation)
			reconcileOpts.Completed().Apply(&azurecontrolplane.DefaultAddOptions.IgnoreOperationAnnotation)
			reconcileOpts.Completed().Apply(&azureworker.DefaultAddOptions.IgnoreOperationAnnotation)
			reconcileOpts.Completed().Apply(&azurebastion.DefaultAddOptions.IgnoreOperationAnnotation)
			workerCtrlOpts.Completed().Apply(&azureworker.DefaultAddOptions.Controller)

			if _, err := webhookOptions.Completed().AddToManager(ctx, mgr); err != nil {
				return fmt.Errorf("could not add webhooks to manager: %w", err)
			}

			if err := controllerSwitches.Completed().AddToManager(mgr); err != nil {
				return fmt.Errorf("could not add controllers to manager: %w", err)
			}

			if err := mgr.AddReadyzCheck("informer-sync", gardenerhealthz.NewCacheSyncHealthz(mgr.GetCache())); err != nil {
				return fmt.Errorf("could not add readycheck for informers: %w", err)
			}

			if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
				return fmt.Errorf("could not add health check to manager: %w", err)
			}

			if err := mgr.AddReadyzCheck("webhook-server", mgr.GetWebhookServer().StartedChecker()); err != nil {
				return fmt.Errorf("could not add ready check for webhook server to manager: %w", err)
			}

			if err := mgr.Start(ctx); err != nil {
				return fmt.Errorf("error running manager: %w", err)
			}

			return nil
		},
	}

	verflag.AddFlags(cmd.Flags())
	aggOption.AddFlags(cmd.Flags())

	return cmd
}

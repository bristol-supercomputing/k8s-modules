package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/controller"
)

func main() {
	var modulesNamespace, metricsAddr, probeAddr string
	var leaderElect bool
	flag.StringVar(&modulesNamespace, "modules-namespace", "module-system", "Namespace holding PVCs for pvc-sourced modules.")
	flag.BoolVar(&leaderElect, "leader-elect", true, "Enable leader election.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Metrics endpoint address; 0 disables it.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Health probe endpoint address.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		log.Error(err, "scheme")
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "module-controller.modules.brics.ac.uk",
		Cache:                  controller.CacheOptions(modulesNamespace),
	})
	if err != nil {
		log.Error(err, "creating manager")
		os.Exit(1)
	}
	if err := (&controller.ModuleReconciler{Client: mgr.GetClient(), ModulesNamespace: modulesNamespace}).SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up controller")
		os.Exit(1)
	}
	if err := (&controller.NodeLabelReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		log.Error(err, "setting up node label controller")
		os.Exit(1)
	}
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "healthz")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "readyz")
		os.Exit(1)
	}
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "running manager")
		os.Exit(1)
	}
}

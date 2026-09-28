package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1 "github.com/isambard-sc/k8s-modules/api/v1alpha1"
	"github.com/isambard-sc/k8s-modules/internal/controller"
)

const modulesNamespace = "module-system"

var (
	restCfg *rest.Config
	k8s     client.Client
	ctx     context.Context
	scheme  = runtime.NewScheme()
	chart   = filepath.Join("..", "..", "deploy", "helm", "k8s-modules")
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctrl.SetLogger(zap.New(zap.WriteTo(io.Discard)))
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(chart, "crds")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintln(os.Stderr, "envtest start:", err)
		return 1
	}
	defer func() { _ = env.Stop() }()
	restCfg = cfg

	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1.AddToScheme(scheme)
	if k8s, err = client.New(cfg, client.Options{Scheme: scheme}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := k8s.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: modulesNamespace}}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Cache:                  controller.CacheOptions(modulesNamespace),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := (&controller.ModuleReconciler{Client: mgr.GetClient(), ModulesNamespace: modulesNamespace}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := (&controller.NodeLabelReconciler{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	go func() { _ = mgr.Start(ctx) }()

	if err := installPolicies(); err != nil {
		fmt.Fprintln(os.Stderr, "installing policies:", err)
		return 1
	}
	return m.Run()
}

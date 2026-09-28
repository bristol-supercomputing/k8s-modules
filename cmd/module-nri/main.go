// Command module-nri is the module system's NRI plugin. It runs as a
// DaemonSet on every node.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/containerd/nri/pkg/stub"
	"github.com/go-logr/logr"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1 "github.com/bristol-supercomputing/k8s-modules/api/v1alpha1"
	"github.com/bristol-supercomputing/k8s-modules/internal/nri"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install-hook" {
		if err := installHook(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "install-hook:", err)
			os.Exit(1)
		}
		return
	}
	var (
		nodeName, podUID, hostRoot, hookPath, pvcDir, pvcs, socket, idx string
		interval                                                        time.Duration
	)
	flag.StringVar(&nodeName, "node-name", os.Getenv("NODE_NAME"), "This node's name.")
	flag.StringVar(&podUID, "pod-uid", os.Getenv("POD_UID"), "This pod's UID, to pick our own PVC mounts.")
	flag.StringVar(&hostRoot, "host-root", "/host", "Where the host root filesystem is mounted.")
	flag.StringVar(&hookPath, "hook-path", "/opt/brics-modules/bin/module-hook", "Host path of module-hook.")
	flag.StringVar(&pvcDir, "pvc-dir", "/pvcs", "Directory holding one mount per configured claim.")
	flag.StringVar(&pvcs, "pvcs", "", "Comma-separated claims mounted under --pvc-dir.")
	flag.StringVar(&socket, "nri-socket", "/var/run/nri/nri.sock", "NRI socket.")
	flag.StringVar(&idx, "plugin-index", "50", "NRI plugin index.")
	flag.DurationVar(&interval, "discovery-interval", 10*time.Minute, "Host module rediscovery interval.")
	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("module-nri")
	if nodeName == "" {
		log.Error(nil, "--node-name or NODE_NAME is required")
		os.Exit(1)
	}
	if err := run(log, nodeName, podUID, hostRoot, hookPath, pvcDir, pvcs, socket, idx, interval); err != nil {
		log.Error(err, "exiting")
		os.Exit(1)
	}
}

func run(log logr.Logger, nodeName, podUID, hostRoot, hookPath, pvcDir, pvcs, socket, idx string, interval time.Duration) error {
	ctx := ctrl.SetupSignalHandler()
	cfg := ctrl.GetConfigOrDie()
	scheme := kruntime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := v1.AddToScheme(scheme); err != nil {
		return err
	}
	modules, err := cache.New(cfg, cache.Options{Scheme: scheme})
	if err != nil {
		return err
	}
	trigger := make(chan struct{}, 1)
	poke := func(obj any) {
		if m, ok := obj.(*v1.Module); ok && m.Spec.Source.Host == nil {
			return
		}
		select {
		case trigger <- struct{}{}:
		default:
		}
	}
	inf, err := modules.GetInformer(ctx, &v1.Module{})
	if err != nil {
		return err
	}
	if _, err := inf.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc:    poke,
		DeleteFunc: func(obj any) { poke(nil) },
	}); err != nil {
		return err
	}
	go func() { _ = modules.Start(ctx) }()
	if !modules.WaitForCacheSync(ctx) {
		return fmt.Errorf("module cache did not sync")
	}
	writer, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	state := &nri.HostState{}
	d := &nri.Discoverer{HostRoot: hostRoot, Arch: runtime.GOARCH, NodeName: nodeName,
		Reader: modules, Writer: writer, State: state, Log: log}
	if err := d.RunOnce(ctx); err != nil {
		log.Error(err, "initial discovery")
	}
	go d.Run(ctx, interval, trigger)

	claims := map[string]bool{}
	for _, c := range strings.Split(pvcs, ",") {
		if c = strings.TrimSpace(c); c != "" {
			claims[c] = true
		}
	}
	p := &nri.Plugin{Log: log, Adjuster: &nri.Adjuster{
		Catalogue: nri.CacheCatalogue{Reader: modules},
		Hosts:     state,
		PVCs: &nri.MountinfoPVCs{Claims: claims, Dir: pvcDir,
			SelfMountinfo: "/proc/self/mountinfo", HostMountinfo: filepath.Join(hostRoot, "proc/1/mountinfo"), PodUID: podUID},
		NodeName: nodeName,
		HookPath: hookPath,
	}}
	s, err := stub.New(p, stub.WithPluginName(v1.NRIPluginName), stub.WithPluginIdx(idx), stub.WithSocketPath(socket))
	if err != nil {
		return err
	}
	// Run returns when containerd drops the connection (for example on a
	// containerd restart); exiting lets kubelet restart us to reconnect.
	if err := s.Run(ctx); err != nil {
		return err
	}
	return fmt.Errorf("NRI connection closed")
}

// installHook copies module-hook and the trusted static ldconfig it runs
// from this image into the host directory the OCI hook path points at.
func installHook(args []string) error {
	fs := flag.NewFlagSet("install-hook", flag.ContinueOnError)
	to := fs.String("to", "/opt/brics-modules/bin", "host directory to install into")
	from := fs.String("from-dir", "/", "directory in this image holding module-hook and ldconfig")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := os.MkdirAll(*to, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"module-hook", "ldconfig"} {
		if err := installFile(filepath.Join(*from, name), *to, name); err != nil {
			return err
		}
	}
	return nil
}

// installFile copies src to dir/name atomically, so a running hook never
// sees a half-written binary.
func installFile(src, dir, name string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, "."+name+"-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}

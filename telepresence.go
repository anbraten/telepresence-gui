package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Domain types
// ---------------------------------------------------------------------------

type ConnectionStatus struct {
	Connected      bool   `json:"connected"`
	Context        string `json:"context"`
	Namespace      string `json:"namespace"`
	Version        string `json:"version"`
	RootDaemon     string `json:"rootDaemon"`
	UserDaemon     string `json:"userDaemon"`
	InterceptCount int    `json:"interceptCount"`
}

type Workload struct {
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	Type        string           `json:"type"`
	Intercepted bool             `json:"intercepted"`
	Intercept   *ActiveIntercept `json:"intercept,omitempty"`
}

type ActiveIntercept struct {
	Name       string `json:"name"`
	Client     string `json:"client"`
	LocalPort  int    `json:"localPort"`
	RemotePort int    `json:"remotePort"`
	Namespace  string `json:"namespace"`
}

type InterceptRequest struct {
	Workload   string `json:"workload"`
	Namespace  string `json:"namespace"`
	LocalPort  string `json:"localPort"`
	RemotePort string `json:"remotePort"`
	EnvFile    string `json:"envFile"`
	MountPath  string `json:"mountPath"`
}

// ---------------------------------------------------------------------------
// Process manager — keeps track of long-running intercept subprocesses
// ---------------------------------------------------------------------------

type processManager struct {
	mu    sync.Mutex
	procs map[string]*exec.Cmd
}

var procMgr = &processManager{procs: make(map[string]*exec.Cmd)}

func (pm *processManager) add(name string, cmd *exec.Cmd) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.procs[name] = cmd
}

func (pm *processManager) remove(name string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.procs, name)
}

// ---------------------------------------------------------------------------
// Telepresence CLI wrapper
// ---------------------------------------------------------------------------

// run executes a telepresence command and returns stdout, stderr, error.
func tpRun(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "telepresence", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// BinaryExists returns true if the telepresence binary is on PATH.
func TpBinaryExists() bool {
	_, err := exec.LookPath("telepresence")
	return err == nil
}

// ---------------------------------------------------------------------------
// Status
// ---------------------------------------------------------------------------

// Raw JSON shape returned by `telepresence status --output json`
type tpStatusJSON struct {
	RootDaemon *struct {
		Running bool   `json:"running"`
		Version string `json:"version"`
	} `json:"root_daemon"`
	UserDaemon *struct {
		Running           bool   `json:"running"`
		KubernetesContext string `json:"kubernetes_context"`
		Namespace         string `json:"namespace"`
		Status            string `json:"status"`
		Intercepts        []struct {
			Name string `json:"name"`
		} `json:"intercepts"`
	} `json:"user_daemon"`
}

func GetStatus(ctx context.Context) ConnectionStatus {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	stdout, _, err := tpRun(ctx, "status", "--output", "json")
	if err != nil {
		return ConnectionStatus{Connected: false}
	}

	var raw tpStatusJSON
	if jsonErr := json.Unmarshal([]byte(stdout), &raw); jsonErr != nil {
		// Fallback: parse text output
		return parseStatusText(stdout)
	}

	cs := ConnectionStatus{}
	if raw.RootDaemon != nil {
		cs.Version = raw.RootDaemon.Version
		if raw.RootDaemon.Running {
			cs.RootDaemon = "running"
		} else {
			cs.RootDaemon = "stopped"
		}
	}
	if raw.UserDaemon != nil {
		if raw.UserDaemon.Running {
			cs.UserDaemon = "running"
		} else {
			cs.UserDaemon = "stopped"
		}
		cs.Context = raw.UserDaemon.KubernetesContext
		cs.Namespace = raw.UserDaemon.Namespace
		cs.Connected = strings.EqualFold(raw.UserDaemon.Status, "connected")
		cs.InterceptCount = len(raw.UserDaemon.Intercepts)
	}
	return cs
}

func parseStatusText(text string) ConnectionStatus {
	cs := ConnectionStatus{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.Contains(lower, "connected") {
			cs.Connected = true
		}
		if strings.HasPrefix(lower, "kubernetes context:") {
			cs.Context = strings.TrimSpace(strings.TrimPrefix(line, "Kubernetes context:"))
		}
		if strings.HasPrefix(lower, "namespace:") {
			cs.Namespace = strings.TrimSpace(strings.TrimPrefix(line, "Namespace:"))
		}
	}
	return cs
}

// ---------------------------------------------------------------------------
// List workloads
// ---------------------------------------------------------------------------

// Raw JSON shape from `telepresence list --output json`
type tpListJSON struct {
	Stdout []struct {
		UID                  string `json:"uid"`
		Name                 string `json:"name"`
		Namespace            string `json:"namespace"`
		WorkloadResourceType string `json:"workload_resource_type"`
		Intercept            *struct {
			Client     string `json:"client"`
			LocalPort  int    `json:"localPort"`
			RemotePort int    `json:"servicePortIdentifier"`
		} `json:"intercept"`
	} `json:"stdout"`
}

func ListWorkloads(ctx context.Context, namespace string) ([]Workload, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	args := []string{"list", "--output", "json"}
	if namespace != "" && namespace != "all" {
		args = append(args, "--namespace", namespace)
	}

	stdout, stderr, err := tpRun(ctx, args...)
	if err != nil {
		// telepresence list sometimes exits non-zero but still outputs data
		if stdout == "" {
			return nil, fmt.Errorf("telepresence list failed: %s", stderr)
		}
	}

	var raw tpListJSON
	jsonErr := json.Unmarshal([]byte(stdout), &raw)
	if jsonErr != nil {
		return nil, fmt.Errorf("failed to parse telepresence list output: %w", jsonErr)
	}

	workloads := make([]Workload, 0, len(raw.Stdout))
	for _, w := range raw.Stdout {
		wl := Workload{
			Name:        w.Name,
			Namespace:   w.Namespace,
			Type:        w.WorkloadResourceType,
			Intercepted: w.Intercept != nil,
		}
		if w.Intercept != nil {
			wl.Intercept = &ActiveIntercept{
				Name:       w.Name,
				Client:     w.Intercept.Client,
				LocalPort:  w.Intercept.LocalPort,
				RemotePort: w.Intercept.RemotePort,
				Namespace:  w.Namespace,
			}
		}
		workloads = append(workloads, wl)
	}
	return workloads, nil
}

// ---------------------------------------------------------------------------
// Intercept
// ---------------------------------------------------------------------------

// StartIntercept starts `telepresence intercept` in a background goroutine.
// The intercept process is long-lived; we track it so we can surface errors.
func StartIntercept(req InterceptRequest) error {
	args := []string{
		"intercept", req.Workload,
		"--namespace", req.Namespace,
	}
	if req.LocalPort != "" || req.RemotePort != "" {
		portSpec := req.LocalPort
		if req.RemotePort != "" {
			portSpec = portSpec + ":" + req.RemotePort
		}
		args = append(args, "--port", portSpec)
	}
	if req.EnvFile != "" {
		args = append(args, "--env-file", req.EnvFile)
	}
	if req.MountPath != "" {
		args = append(args, "--mount", req.MountPath)
	}

	cmd := exec.Command("telepresence", args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start intercept: %w", err)
	}
	procMgr.add(req.Workload, cmd)

	// Reap the process when it finishes so we don't leak goroutines
	go func() {
		_ = cmd.Wait()
		procMgr.remove(req.Workload)
	}()
	return nil
}

// LeaveIntercept calls `telepresence leave <name>`.
func LeaveIntercept(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, stderr, err := tpRun(ctx, "leave", name)
	if err != nil {
		return fmt.Errorf("leave failed: %s", stderr)
	}
	procMgr.remove(name)
	return nil
}

// ---------------------------------------------------------------------------
// Connect / Quit
// ---------------------------------------------------------------------------

func Connect(ctx context.Context, namespace string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"connect"}
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	_, stderr, err := tpRun(ctx, args...)
	if err != nil {
		return fmt.Errorf("connect failed: %s", stderr)
	}
	return nil
}

func Quit(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, stderr, err := tpRun(ctx, "quit")
	if err != nil {
		return fmt.Errorf("quit failed: %s", stderr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Namespaces (via kubectl)
// ---------------------------------------------------------------------------

func ListNamespaces(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "kubectl", "get", "namespaces",
		"-o", "jsonpath={.items[*].metadata.name}")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return []string{"default"}, nil
	}
	parts := strings.Fields(out.String())
	if len(parts) == 0 {
		return []string{"default"}, nil
	}
	return parts, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// dbg is the debug logger; it is a no-op until --debug is passed.
var dbg = log.New(os.Stderr, "\033[2m[dbg] ", log.Ltime|log.Lmicroseconds)

func debugf(format string, a ...any) {
	if flagDebug {
		dbg.Printf(format+"\033[0m", a...)
	}
}

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
	Name          string `json:"name"`
	Client        string `json:"client"`
	LocalPort     int    `json:"localPort"`
	RemotePort    int    `json:"remotePort"`
	Namespace     string `json:"namespace"`
	TargetHost    string `json:"targetHost,omitempty"`
	ContainerPort int    `json:"containerPort,omitempty"`
	Protocol      string `json:"protocol,omitempty"`
	Mechanism     string `json:"mechanism,omitempty"`
	Replace       bool   `json:"replace,omitempty"`
	Wiretap       bool   `json:"wiretap,omitempty"`
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
// Telepresence CLI wrapper
// ---------------------------------------------------------------------------

// run executes a telepresence command and returns stdout, stderr, error.
func tpRun(ctx context.Context, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "telepresence", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	debugf("▶ telepresence %s", strings.Join(args, " "))
	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Round(time.Millisecond)
	out := strings.TrimSpace(stdout.String())
	err2 := strings.TrimSpace(stderr.String())
	if flagDebug {
		if out != "" {
			for _, line := range strings.Split(out, "\n") {
				dbg.Printf("  stdout: %s\033[0m", line)
			}
		}
		if err2 != "" {
			for _, line := range strings.Split(err2, "\n") {
				dbg.Printf("  stderr: %s\033[0m", line)
			}
		}
		if err != nil {
			dbg.Printf("  exit error after %s: %v\033[0m", elapsed, err)
		} else {
			dbg.Printf("  ok (%s)\033[0m", elapsed)
		}
	}
	return out, err2, err
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
			Name   string `json:"name"`
			Client string `json:"client"`
		} `json:"intercepts"`
	} `json:"user_daemon"`
}

func GetStatus(ctx context.Context) ConnectionStatus {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
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
		cs.Connected = cs.Context != "" && cs.Namespace != "" && cs.UserDaemon == "running"
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
			Spec struct {
				Name            string `json:"name"`
				Client          string `json:"client"`
				Agent           string `json:"agent"`
				Mechanism       string `json:"mechanism"`
				TargetHost      string `json:"target_host"`
				PortIdentifier  string `json:"port_identifier"`
				ServicePort     int    `json:"service_port"`
				ServicePortName string `json:"service_port_name"`
				ServiceUID      string `json:"service_uid"`
				Protocol        string `json:"protocol"`
				ContainerName   string `json:"container_name"`
				ContainerPort   int    `json:"container_port"`
				TargetPort      int    `json:"target_port"`
				Replace         bool   `json:"replace"`
				Wiretap         bool   `json:"wiretap"`
			} `json:"spec"`
			Client      string            `json:"client"`
			LocalPort   int               `json:"localPort"`
			RemotePort  int               `json:"servicePortIdentifier"`
			Environment map[string]string `json:"environment,omitempty"`
		} `json:"intercept_info,omitempty"`
		AgentVersion string `json:"agent_version"`
	} `json:"stdout"`
}

func ListWorkloads(ctx context.Context, namespace string) ([]Workload, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
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
				Name:          w.Name,
				Client:        w.Intercept.Client,
				LocalPort:     w.Intercept.LocalPort,
				RemotePort:    w.Intercept.RemotePort,
				Namespace:     w.Namespace,
				TargetHost:    w.Intercept.Spec.TargetHost,
				ContainerPort: w.Intercept.Spec.ContainerPort,
				Protocol:      w.Intercept.Spec.Protocol,
				Mechanism:     w.Intercept.Spec.Mechanism,
				Replace:       w.Intercept.Spec.Replace,
				Wiretap:       w.Intercept.Spec.Wiretap,
			}
		}
		workloads = append(workloads, wl)
	}
	return workloads, nil
}

// ---------------------------------------------------------------------------
// Intercept
// ---------------------------------------------------------------------------

// StartIntercept runs `telepresence intercept` and waits for it to complete.
// On error the JSON output contains {cmd, error}; on success it exits 0 with no output.
func StartIntercept(ctx context.Context, req InterceptRequest) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{
		"intercept", req.Workload,
		"--namespace", req.Namespace,
		"--output", "json",
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

	stdout, stderr, err := tpRun(ctx, args...)
	if err != nil {
		// Try to extract the error message from JSON output first
		var result struct {
			Error string `json:"error"`
		}
		if stdout != "" {
			if jsonErr := json.Unmarshal([]byte(stdout), &result); jsonErr == nil && result.Error != "" {
				return fmt.Errorf("intercept failed: %s", result.Error)
			}
		}
		if stderr != "" {
			return fmt.Errorf("intercept failed: %s", stderr)
		}
		return fmt.Errorf("intercept failed: %w", err)
	}
	return nil
}

// LeaveIntercept calls `telepresence leave <name>`.
func LeaveIntercept(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, stderr, err := tpRun(ctx, "leave", name)
	if err != nil {
		return fmt.Errorf("leave failed: %s", stderr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Connect / Quit
// ---------------------------------------------------------------------------

func Connect(ctx context.Context, namespace string) error {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
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
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
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
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	kArgs := []string{"get", "namespaces", "-o", "jsonpath={.items[*].metadata.name}"}
	debugf("▶ kubectl %s", strings.Join(kArgs, " "))
	start := time.Now()
	cmd := exec.CommandContext(ctx, "kubectl", kArgs...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		debugf("  kubectl exit error after %s: %v\033[0m", time.Since(start).Round(time.Millisecond), err)
		return []string{"default"}, nil
	}
	debugf("  ok (%s) stdout: %s", time.Since(start).Round(time.Millisecond), strings.TrimSpace(out.String()))
	parts := strings.Fields(out.String())
	if len(parts) == 0 {
		return []string{"default"}, nil
	}
	return parts, nil
}

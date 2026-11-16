package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// realDeployments reads the workloads in a preview namespace through kubectl.
//
// kubectl rather than client-go on purpose: the task images already carry kubectl
// (the task documents it as part of the consumer-script contract), and it means
// this binary needs no kubeconfig plumbing, no generated API types, and no
// RBAC beyond what the step already has. The replica counts and image come from
// the same object the gate reads.
func realDeployments(ctx context.Context, namespace string) ([]Deployment, error) {
	out, err := exec.CommandContext(ctx, "kubectl", "-n", namespace, "get", "deploy", "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get deploy -n %s: %w", namespace, err)
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct {
							Image string `json:"image"`
							// Only httpGet probes are readable as a path; an exec or
							// tcpSocket probe leaves this empty and the caller falls
							// back, which is honest.
							ReadinessProbe *struct {
								HTTPGet *struct {
									Path string `json:"path"`
								} `json:"httpGet"`
							} `json:"readinessProbe"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
				Replicas      int `json:"replicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parsing kubectl output: %w", err)
	}

	deps := make([]Deployment, 0, len(payload.Items))
	for _, it := range payload.Items {
		d := Deployment{
			Name:     it.Metadata.Name,
			Ready:    it.Status.ReadyReplicas,
			Replicas: it.Status.Replicas,
			AppName:  it.Metadata.Labels["app.kubernetes.io/name"],
		}
		if d.AppName == "" {
			d.AppName = it.Metadata.Labels["app"]
		}
		if len(it.Spec.Template.Spec.Containers) > 0 {
			c := it.Spec.Template.Spec.Containers[0]
			d.Image = c.Image
			if c.ReadinessProbe != nil && c.ReadinessProbe.HTTPGet != nil {
				d.ReadinessPath = c.ReadinessProbe.HTTPGet.Path
			}
		}
		if d.Replicas == 0 && it.Spec.Replicas != nil {
			d.Replicas = *it.Spec.Replicas
		}
		deps = append(deps, d)
	}
	return deps, nil
}

// serviceExists reports whether the preview namespace has a Service by that name.
// Used by the wait to avoid building a URL for something that is not there.
func serviceExists(ctx context.Context, namespace, name string) bool {
	out, err := exec.CommandContext(ctx, "kubectl", "-n", namespace, "get", "svc", "-o", "name").Output()
	if err != nil {
		return false
	}
	want := "service/" + name
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// portOf returns a Service's first exposed port, or 0.
func portOf(ctx context.Context, namespace, name string) int {
	out, err := exec.CommandContext(ctx, "kubectl", "-n", namespace, "get", "svc", name,
		"-o", "jsonpath={.spec.ports[0].port}").Output()
	if err != nil {
		return 0
	}
	p, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return p
}

// realDo issues one HTTP request. Used by the setup verbs, which talk to the
// preview's own Hydra admin rather than to the public ingress.
func realDo(ctx context.Context, method, url string, body []byte) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, out, nil
}

// hydraAdminFor finds the Hydra admin Service in a preview namespace.
//
// By name, not "the first Service": a preview also runs its app and its peer, and the
// admin API is the one called hydra-admin or preview-hydra-admin depending on the
// chart's release prefix. An empty answer means the preview has no issuer of its own,
// which is a finding rather than something to work around.
func hydraAdminFor(ctx context.Context, namespace string) string {
	out, err := exec.CommandContext(ctx, "kubectl", "-n", namespace, "get", "svc", "-o", "json").Output()
	if err != nil {
		return ""
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return ""
	}
	for _, it := range payload.Items {
		n := it.Metadata.Name
		if !strings.HasSuffix(n, "hydra-admin") {
			continue
		}
		port := 4445
		if len(it.Spec.Ports) > 0 {
			port = it.Spec.Ports[0].Port
		}
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", n, namespace, port)
	}
	return ""
}

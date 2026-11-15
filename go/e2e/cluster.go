package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

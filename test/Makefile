# Pipeline Task Test Harness
# Tests catalog tasks against the pipeline-test namespace (dry-run mode — no PR posts)

NAMESPACE := pipeline-test
CONTEXT :=

ifdef AZURE
  CONTEXT := --context modern-burro-admin
endif

help:
	@# Default target — show usage
	@echo ""
	@echo "  Leartech Pipeline Catalog — Test Harness"
	@echo "  ========================================="
	@echo ""
	@echo "  Tests scan scripts against the pipeline-test namespace."
	@echo "  Dry-run mode: prints formatted PR comments, no API calls."
	@echo ""
	@echo "  Usage:  make -f test/Makefile <target> [AZURE=1]"
	@echo ""
	@echo "  Targets:"
	@echo "    test-static     Run Gitleaks + Semgrep against test data"
	@echo "    test-image      Run Grype dependency scan against test data"
	@echo "    test-dynamic    Run Nuclei + Nikto + Nmap against pipeline-test nginx"
	@echo "    test-all        Run all scan tests sequentially"
	@echo ""
	@echo "    logs TASK=x     Tail logs for a test (x = static, image, or dynamic)"
	@echo "    clean           Delete all test pods"
	@echo ""
	@echo "  Options:"
	@echo "    AZURE=1         Run against Azure cluster (modern-burro-admin context)"
	@echo ""
	@echo "  Examples:"
	@echo "    make -f test/Makefile test-static            # GCP static scan"
	@echo "    make -f test/Makefile test-dynamic AZURE=1   # Azure dynamic scan"
	@echo "    make -f test/Makefile test-all               # All scans on GCP"
	@echo "    make -f test/Makefile logs TASK=dynamic      # Watch dynamic scan logs"
	@echo ""
	@echo "  Prerequisites:"
	@echo "    - kubectl configured with cluster access"
	@echo "    - pipeline-test namespace deployed (charts/pipeline-test in cluster repo)"
	@echo "    - security-tools image built (ghcr.io/mikelear/security-tools:latest)"
	@echo ""

test-static: clean-static
	@echo ""
	@echo "=== Static Security Scan (Gitleaks + Semgrep) ==="
	@echo "Namespace: $(NAMESPACE)"
	@echo ""
	kubectl $(CONTEXT) apply -f test/taskrun-static-scan.yaml
	@echo "Waiting for pod..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/static-scan-test -n $(NAMESPACE) --timeout=300s 2>/dev/null || true
	@echo ""
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) static-scan-test

test-image: clean-image
	@echo ""
	@echo "=== Dependency Scan (Grype) ==="
	@echo "Namespace: $(NAMESPACE)"
	@echo ""
	kubectl $(CONTEXT) apply -f test/taskrun-image-scan.yaml
	@echo "Waiting for pod..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/image-scan-test -n $(NAMESPACE) --timeout=300s 2>/dev/null || true
	@echo ""
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) image-scan-test

test-dynamic: clean-dynamic
	@echo ""
	@echo "=== Dynamic Security Scan (Nuclei + Nikto + Nmap) ==="
	@echo "Namespace: $(NAMESPACE)"
	@echo "This takes a few minutes..."
	@echo ""
	kubectl $(CONTEXT) apply -f test/taskrun-dynamic-scan.yaml
	@echo "Waiting for pod (up to 10 minutes)..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/dynamic-scan-test -n $(NAMESPACE) --timeout=600s 2>/dev/null || true
	@echo ""
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) dynamic-scan-test

test-all: test-static test-image test-dynamic
	@echo ""
	@echo "=== All tests complete ==="

clean-static:
	@kubectl $(CONTEXT) delete pod static-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean-image:
	@kubectl $(CONTEXT) delete pod image-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean-dynamic:
	@kubectl $(CONTEXT) delete pod dynamic-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean: clean-static clean-image clean-dynamic
	@echo "All test pods cleaned up"

logs:
	kubectl $(CONTEXT) logs -n $(NAMESPACE) $(TASK)-scan-test -f

.PHONY: help test-static test-image test-dynamic test-all clean clean-static clean-image clean-dynamic logs

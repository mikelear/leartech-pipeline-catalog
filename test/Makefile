# Pipeline Task Test Harness
# Tests catalog tasks against the pipeline-test namespace (dry-run mode — no PR posts)
#
# Usage:
#   make test-static        # Test Gitleaks + Semgrep
#   make test-image          # Test Grype dependency scan
#   make test-dynamic        # Test Nuclei + Nikto + Nmap
#   make test-all            # Run all tests
#   make clean               # Delete test pods
#   make logs TASK=dynamic   # Tail logs for a test

NAMESPACE := pipeline-test
CONTEXT :=

ifdef AZURE
  CONTEXT := --context modern-burro-admin
endif

.PHONY: test-static test-image test-dynamic test-all clean logs

test-static: clean-static
	kubectl $(CONTEXT) apply -f test/taskrun-static-scan.yaml
	@echo "Waiting for pod..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/static-scan-test -n $(NAMESPACE) --timeout=300s 2>/dev/null || true
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) static-scan-test

test-image: clean-image
	kubectl $(CONTEXT) apply -f test/taskrun-image-scan.yaml
	@echo "Waiting for pod..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/image-scan-test -n $(NAMESPACE) --timeout=300s 2>/dev/null || true
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) image-scan-test

test-dynamic: clean-dynamic
	kubectl $(CONTEXT) apply -f test/taskrun-dynamic-scan.yaml
	@echo "Waiting for pod (this takes a few minutes)..."
	@kubectl $(CONTEXT) wait --for=jsonpath='{.status.phase}'=Succeeded pod/dynamic-scan-test -n $(NAMESPACE) --timeout=600s 2>/dev/null || true
	@echo "=== Results ==="
	@kubectl $(CONTEXT) logs -n $(NAMESPACE) dynamic-scan-test

test-all: test-static test-image test-dynamic

clean-static:
	@kubectl $(CONTEXT) delete pod static-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean-image:
	@kubectl $(CONTEXT) delete pod image-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean-dynamic:
	@kubectl $(CONTEXT) delete pod dynamic-scan-test -n $(NAMESPACE) --ignore-not-found=true 2>/dev/null

clean: clean-static clean-image clean-dynamic

logs:
	kubectl $(CONTEXT) logs -n $(NAMESPACE) $(TASK)-scan-test -f

#!/bin/bash
set -uo pipefail
curl -sS -o /dev/null -w '%{http_code}\n' https://api.mortgagesourcing.com/mcp

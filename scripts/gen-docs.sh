#!/usr/bin/env bash
# Regenerate docs/ (tfplugindocs) and re-apply the just-the-docs nav front
# matter that tfplugindocs itself doesn't know about. Run this any time a
# resource/data-source schema description changes.
#
# Requires tfplugindocs on PATH:
#   go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@latest
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

tfplugindocs generate
python3 scripts/postprocess_docs.py

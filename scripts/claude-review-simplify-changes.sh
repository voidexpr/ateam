#!/usr/bin/env bash
set -euo pipefail

ateam exec "/code-review medium --fix /simplify" --agent claude

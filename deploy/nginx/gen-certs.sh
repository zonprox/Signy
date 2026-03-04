#!/bin/bash
# Generate self-signed certificates for local development.
# Usage: ./gen-certs.sh

set -euo pipefail

CERT_DIR="$(cd "$(dirname "$0")" && pwd)/certs"
mkdir -p "$CERT_DIR"

echo "Generating self-signed certificate in ${CERT_DIR}..."

openssl req -x509 -nodes -days 365 \
  -newkey rsa:2048 \
  -keyout "${CERT_DIR}/server.key" \
  -out "${CERT_DIR}/server.crt" \
  -subj "/C=US/ST=Dev/L=Local/O=Signy/OU=Dev/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

echo "Certificate generated:"
echo "  Key:  ${CERT_DIR}/server.key"
echo "  Cert: ${CERT_DIR}/server.crt"
echo ""
echo "For production, use Let's Encrypt or another CA."

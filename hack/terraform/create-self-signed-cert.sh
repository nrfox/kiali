#!/bin/bash

CERT_DIR=$(mktemp -d)

# Now generate the oidc server cert from the root CA
cat <<EOF > "$CERT_DIR"/req.cnf
[req]
req_extensions = req_ext
x509_extensions = v3_req
distinguished_name = req_distinguished_name
prompt = no

[req_distinguished_name]
countryName = XX
stateOrProvinceName = N/A
localityName = N/A
organizationName = N/A
commonName = N/A

[ v3_req ]
subjectAltName = @alt_names

[ req_ext ]
subjectAltName = @alt_names

[alt_names]
IP.1 = $1
EOF

openssl req -x509 -nodes -days 365 -newkey rsa:2048 -keyout "$CERT_DIR"/key.pem -out "$CERT_DIR"/cert.pem -config "$CERT_DIR"/req.cnf

kubectl create secret tls kiali-tls --cert="$CERT_DIR"/cert.pem --key="$CERT_DIR"/key.pem -n istio-system

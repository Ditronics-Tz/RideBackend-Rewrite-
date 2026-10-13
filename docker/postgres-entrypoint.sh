#!/bin/sh
set -eu

tls_dir=/run/postgresql/tls
ca_dir=/run/postgresql/ca
mkdir -p "$tls_dir" "$ca_dir"
openssl req -x509 -newkey rsa:3072 -nodes -sha256 -days 365 \
  -subj "/CN=db" -addext "subjectAltName=DNS:db" \
  -keyout "$tls_dir/server.key" -out "$tls_dir/server.crt"
cp "$tls_dir/server.crt" "$ca_dir/server.crt"
chown postgres:postgres "$tls_dir/server.key" "$tls_dir/server.crt"
chown root:root "$ca_dir/server.crt"
chmod 0600 "$tls_dir/server.key"
chmod 0644 "$tls_dir/server.crt"
chmod 0644 "$ca_dir/server.crt"

exec /usr/local/bin/docker-entrypoint.sh "$@" \
  -c ssl=on \
  -c ssl_cert_file="$tls_dir/server.crt" \
  -c ssl_key_file="$tls_dir/server.key"

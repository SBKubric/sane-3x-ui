#!/usr/bin/env bash
# Throwaway lab for research #275: two continuwuity homeservers (hs1.test,
# hs2.test) federating through one nginx front on :443 (SNI split, as in
# ADR 0005), then `go run -tags goolm .` against them. Everything is local.
#
#   ./run.sh            # fresh lab, run the check, tear down
#   KEEP=1 ./run.sh     # leave the containers running afterwards
#
# Secrets (registration tokens, account passwords, crypto stores, recovery
# key) are generated into $WORK and are never committed.
set -euo pipefail
cd "$(dirname "$0")"
WORK=${WORK:-$PWD/.lab}
IMG=${IMG:-forgejo.ellis.link/continuwuation/continuwuity:v26.9.1}
NET=mx275
U="$(id -u):$(id -g)"

cleanup() {
  [ "${KEEP:-}" = 1 ] && return
  docker rm -f mx275-hs1 mx275-hs2 mx275-front >/dev/null 2>&1 || true
  docker network rm $NET >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup
rm -rf "$WORK"; mkdir -p "$WORK"/{tls,hs1/data,hs2/data,state}; chmod 700 "$WORK/state"

# Lab CA + one leaf for all four names.
( cd "$WORK/tls"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout ca.key -out ca.pem -days 2 -subj /CN=lab-ca 2>/dev/null
  openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -keyout leaf.key -out leaf.csr -subj /CN=hs1.test 2>/dev/null
  printf 'subjectAltName=DNS:hs1.test,DNS:matrix.hs1.test,DNS:hs2.test,DNS:matrix.hs2.test\nextendedKeyUsage=serverAuth\n' > ext.cnf
  openssl x509 -req -in leaf.csr -CA ca.pem -CAkey ca.key -CAcreateserial -out leaf.pem -days 2 -extfile ext.cnf 2>/dev/null
  chmod 644 leaf.key )

for n in 1 2; do
  openssl rand -hex 12 > "$WORK/hs$n.token"
  cat > "$WORK/hs$n/conduwuit.toml" <<EOF
[global]
server_name = "hs$n.test"
database_path = "/data"
address = ["0.0.0.0"]
port = 8008
allow_registration = true
registration_token = "$(cat "$WORK/hs$n.token")"
ip_range_denylist = []   # lab only: federation over the private docker network
trusted_servers = []
allow_announcements_check = false
[global.well_known]
server = "matrix.hs$n.test:443"   # delegation: federation goes to :443, not :8448
client = "https://matrix.hs$n.test"
EOF
done

cat > "$WORK/front.conf" <<'EOF'
server {
  listen 443 ssl; http2 on;
  server_name hs1.test matrix.hs1.test;
  ssl_certificate /tls/leaf.pem; ssl_certificate_key /tls/leaf.key;
  client_max_body_size 50m;
  location / { proxy_pass http://hs1:8008; proxy_set_header Host $host; proxy_set_header X-Forwarded-For $remote_addr; }
}
server {
  listen 443 ssl; http2 on;
  server_name hs2.test matrix.hs2.test;
  ssl_certificate /tls/leaf.pem; ssl_certificate_key /tls/leaf.key;
  client_max_body_size 50m;
  location / { proxy_pass http://hs2:8008; proxy_set_header Host $host; proxy_set_header X-Forwarded-For $remote_addr; }
}
EOF

docker network create $NET >/dev/null
for n in 1 2; do
  docker run -d --name mx275-hs$n --network $NET --network-alias hs$n --user "$U" \
    -p 127.0.0.1:${n}8008:8008 -e SSL_CERT_FILE=/tls/ca.pem -e CONTINUWUITY_CONFIG=/etc/conduwuit.toml \
    -v "$WORK/hs$n/conduwuit.toml":/etc/conduwuit.toml:ro -v "$WORK/hs$n/data":/data \
    -v "$WORK/tls/ca.pem":/tls/ca.pem:ro "$IMG" >/dev/null
done
docker run -d --name mx275-front --network $NET \
  --network-alias hs1.test --network-alias matrix.hs1.test --network-alias hs2.test --network-alias matrix.hs2.test \
  -v "$WORK/front.conf":/etc/nginx/conf.d/default.conf:ro -v "$WORK/tls":/tls:ro nginx:1.27-alpine >/dev/null

# The first account needs a one-time token that continuwuity prints on first start.
first_token() {
  for _ in $(seq 60); do
    t=$(docker logs mx275-hs$1 2>&1 | sed 's/\x1b\[[0-9;]*m//g' | grep -o 'registration token [A-Za-z0-9]* ' | head -1 | awk '{print $3}')
    [ -n "$t" ] && { echo "$t"; return; }
    sleep 1
  done
  echo "no first-user token from hs$1" >&2; exit 1
}
T1=$(first_token 1); T2=$(first_token 2)
sleep 15
echo "--- idle homeservers"
docker stats --no-stream --format '{{.Name}} {{.MemUsage}} {{.CPUPerc}}' mx275-hs1 mx275-hs2 mx275-front

docker run --rm --user "$U" -e HOME=/tmp -e GOCACHE=/gocache -e GOMODCACHE=/gomod -e GOFLAGS=-buildvcs=false \
  -e HS1_FIRST_TOKEN="$T1" -e HS2_FIRST_TOKEN="$T2" \
  -e HS1_TOKEN="$(cat "$WORK/hs1.token")" -e HS2_TOKEN="$(cat "$WORK/hs2.token")" \
  -v gomod-3ax:/gomod -v gocache-3ax:/gocache -v "$PWD":/src -v "$WORK/state":/state -w /src --network host \
  golang:1.26-bookworm go run -tags goolm . -state /state "$@"

echo "--- homeservers after the run"
docker stats --no-stream --format '{{.Name}} {{.MemUsage}} {{.CPUPerc}}' mx275-hs1 mx275-hs2 mx275-front
du -sh "$WORK/hs1/data" "$WORK/state"/*.db 2>/dev/null || true

#!/bin/sh
set -eu

mkdir -p /etc/nginx/stream-conf.d

cat > /etc/nginx/stream-conf.d/default.conf <<'EOF'
upstream turn_listener_1 {
    server 172.31.0.43:53478;
}

upstream turn_listener_2 {
    server 172.31.0.44:53479;
}

server {
    listen 53478 udp;
    proxy_pass turn_listener_1;
    proxy_timeout 1h;
}

server {
    listen 53478;
    proxy_pass turn_listener_1;
    proxy_timeout 1h;
}

server {
    listen 53479 udp;
    proxy_pass turn_listener_2;
    proxy_timeout 1h;
}

server {
    listen 53479;
    proxy_pass turn_listener_2;
    proxy_timeout 1h;
}
EOF

for port in $(seq 55000 55099); do
    cat >> /etc/nginx/stream-conf.d/default.conf <<EOF
server {
    listen ${port} udp;
    proxy_pass 172.31.0.43:${port};
    proxy_timeout 1h;
}

server {
    listen ${port};
    proxy_pass 172.31.0.43:${port};
    proxy_timeout 1h;
}
EOF
done

for port in $(seq 55100 55199); do
    cat >> /etc/nginx/stream-conf.d/default.conf <<EOF
server {
    listen ${port} udp;
    proxy_pass 172.31.0.44:${port};
    proxy_timeout 1h;
}

server {
    listen ${port};
    proxy_pass 172.31.0.44:${port};
    proxy_timeout 1h;
}
EOF
done

nginx -t

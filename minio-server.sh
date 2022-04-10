#!/bin/bash -ex
# When testing with minio, add these arguments to shrimp:
# -profile minio -endpoint-url https://localhost:9000/ -no-verify-ssl

mkdir -p minio-data
export MINIO_ROOT_USER=admin
export MINIO_ROOT_PASSWORD=password
minio server minio-data --console-address ":9001"

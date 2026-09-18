#!/usr/bin/env python3
"""p4_anchor_object_verify.py -- read an audit anchor object through the S3 API.

Never read a MinIO object from the data directory: MinIO stores internal bytes
ahead of the payload, so part.1 does not start at the payload and its SHA-256
does not match the recorded hash. This reads the object the way a client does
(GET through the S3 API) and compares its SHA-256 with the hash the platform
recorded in audit_chain_anchors.content_hash.

Usage:
  python3 scripts/s7/p4_anchor_object_verify.py CHAIN YYYY-MM-DD [--expect SHA256]

Prints no credential. Exit 0 when the hash matches (or no --expect given),
1 on mismatch, 2 on a read error.
"""
import hashlib
import sys

ENV = "/home/euripides/projects/blockchain-platform/deploy/.env"
BUCKET = "blockchain-audit"


def load_env(path):
    env = {}
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            k, v = line.split("=", 1)
            env[k.strip()] = v.strip().strip(chr(34)).strip(chr(39))
    return env


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("--")]
    expect = ""
    if "--expect" in sys.argv:
        expect = sys.argv[sys.argv.index("--expect") + 1].strip().lower()
    if len(args) < 2:
        print("usage: p4_anchor_object_verify.py CHAIN YYYY-MM-DD [--expect SHA256]")
        return 2
    chain, date = args[0], args[1]
    key = "audit/%s/%s/anchor.json" % (chain, date)

    import boto3
    from botocore.config import Config

    env = load_env(ENV)
    port = env.get("MINIO_API_PORT", "9000")
    secure = env.get("MINIO_SECURE", "false").lower() == "true"
    endpoint = "http%s://127.0.0.1:%s" % ("s" if secure else "", port)
    s3 = boto3.client(
        "s3",
        endpoint_url=endpoint,
        aws_access_key_id=env["MINIO_ROOT_USER"],
        aws_secret_access_key=env["MINIO_ROOT_PASSWORD"],
        region_name="us-east-1",
        config=Config(signature_version="s3v4", s3={"addressing_style": "path"}),
        verify=False,
    )
    try:
        head = s3.head_object(Bucket=BUCKET, Key=key)
    except Exception as exc:
        print("OBJECT_READ_ERROR bucket=%s key=%s err=%s" % (BUCKET, key, exc))
        return 2
    body = s3.get_object(Bucket=BUCKET, Key=key)["Body"].read()
    digest = hashlib.sha256(body).hexdigest()
    print("bucket=%s key=%s" % (BUCKET, key))
    print("content_length=%d etag=%s last_modified=%s" % (
        head["ContentLength"], head.get("ETag", ""), head.get("LastModified", "")))
    print("read_bytes=%d sha256=%s" % (len(body), digest))
    print("head80=%s" % body[:80].decode("utf-8", "replace").replace(chr(10), " "))
    if expect:
        print("expected_sha256=%s" % expect)
        print("HASH_MATCH=%s" % ("1" if expect == digest else "0"))
        return 0 if expect == digest else 1
    return 0


if __name__ == "__main__":
    sys.exit(main())

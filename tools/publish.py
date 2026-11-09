# SPDX-FileCopyrightText: 2026 Nikolay Govorov
# SPDX-License-Identifier: BSD-3-Clause

"""Publish immutable hrc builds and promote channel manifests in S3."""

import argparse
import hashlib
import json
import mimetypes
import os
import re
import subprocess
import sys
from pathlib import Path

import boto3
from botocore.exceptions import BotoCoreError, ClientError

IMMUTABLE = "public, max-age=31536000, immutable"
CHANNEL = "public, max-age=60, must-revalidate"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def version_key(version):
    match = re.fullmatch(
        r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
        r"(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?"
        r"(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?",
        version,
    )
    if not match:
        raise ValueError(f"Invalid version: {version}")
    prerelease = match[4]
    identifiers = ()
    if prerelease is not None:
        parts = prerelease.split(".")
        if any(not p or (p.isdigit() and len(p) > 1 and p[0] == "0") for p in parts):
            raise ValueError(f"Invalid prerelease: {version}")
        identifiers = tuple((0, int(p)) if p.isdigit() else (1, p) for p in parts)
    return tuple(int(match[i]) for i in (1, 2, 3)), prerelease is None, identifiers


class Storage:
    def __init__(self):
        self.bucket = os.environ["S3_BUCKET"]
        self.client = boto3.client(
            "s3",
            endpoint_url=os.environ["S3_ENDPOINT"],
            aws_access_key_id=os.environ["S3_ACCESS_KEY_ID"],
            aws_secret_access_key=os.environ["S3_SECRET_ACCESS_KEY"],
            region_name="auto",
        )

    def read_versioned(self, key):
        try:
            response = self.client.get_object(Bucket=self.bucket, Key=key)
            return response["Body"].read(), response["ETag"]
        except ClientError as error:
            if error.response["Error"]["Code"] in {"404", "NoSuchKey", "NotFound"}:
                return None, None
            raise

    def read(self, key):
        return self.read_versioned(key)[0]

    @staticmethod
    def conflict(error):
        return error.response["Error"]["Code"] in {
            "409", "412", "ConditionalRequestConflict", "PreconditionFailed",
        }

    def immutable(self, key, data, content_type):
        # Conditional creation is required even after a read: another publisher
        # may have created this object in the meantime.
        try:
            self.client.put_object(
                Bucket=self.bucket, Key=key, Body=data, IfNoneMatch="*",
                ContentType=content_type, CacheControl=IMMUTABLE,
                Metadata={"sha256": digest(data)},
            )
        except ClientError as error:
            if not self.conflict(error):
                raise
            if self.read(key) != data:
                raise ValueError(f"Immutable object has different content: {key}") from None


def verify_signature(directory, keyring):
    subprocess.run(
        ["gpg", "--homedir", str(keyring), "--batch", "--verify",
         str(directory / "SHASUMS256.asc"), str(directory / "SHASUMS256")],
        check=True,
        env={"PATH": os.environ["PATH"]},
    )


def publish(storage, directory, manifest, keyring):
    if keyring is None:
        raise ValueError("--keyring is required when uploading a build")
    prefix = f"hrc/builds/{manifest['version']}"
    names = []
    for artifact in manifest["artifacts"]:
        name = artifact["filename"]
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9.+_-]*", name):
            raise ValueError(f"Invalid artifact filename: {name}")
        if name in names or name in {"manifest.json", "SHASUMS256", "SHASUMS256.asc", "VERSION"}:
            raise ValueError(f"Duplicate or reserved artifact filename: {name}")
        data = (directory / name).read_bytes()
        if len(data) != artifact["size"] or digest(data) != artifact["sha256"]:
            raise ValueError(f"Artifact does not match manifest: {name}")
        names.append(name)
    if not names:
        raise ValueError("Manifest has no artifacts")
    names.append("manifest.json")
    expected_sums = "".join(f"{digest((directory / name).read_bytes())}  {name}\n" for name in sorted(names)).encode()
    if (directory / "SHASUMS256").read_bytes() != expected_sums:
        raise ValueError("SHASUMS256 does not match the complete build")
    verify_signature(directory, keyring)

    # Check existing objects before uploading anything. Retain the first valid
    # signature: signing the same sums again can produce different bytes.
    for name in [*names, "SHASUMS256"]:
        existing = storage.read(f"{prefix}/{name}")
        if existing is not None and existing != (directory / name).read_bytes():
            raise ValueError(f"Immutable object has different content: {prefix}/{name}")
    signature_key = f"{prefix}/SHASUMS256.asc"
    signature = storage.read(signature_key)
    if signature is None:
        try:
            storage.immutable(signature_key, (directory / "SHASUMS256.asc").read_bytes(), "application/pgp-signature")
        except ValueError:
            # Another publisher may have won creation with an equally valid signature.
            signature = storage.read(signature_key)
            if signature is None:
                raise
        else:
            signature = storage.read(signature_key)
    (directory / "SHASUMS256.asc").write_bytes(signature)
    verify_signature(directory, keyring)
    for name in [n for n in names if n != "manifest.json"] + ["SHASUMS256", "manifest.json"]:
        content_type = "application/gzip" if name.endswith(".tar.gz") else mimetypes.guess_type(name)[0] or "text/plain"
        storage.immutable(f"{prefix}/{name}", (directory / name).read_bytes(), content_type)
    print(f"Published {prefix}")


def promote(storage, channel, manifest, raw_manifest):
    key = f"hrc/channels/{channel}.json"
    if storage.read(f"hrc/builds/{manifest['version']}/manifest.json") != raw_manifest:
        raise ValueError("Build manifest must be published before promoting a channel")
    existing, etag = storage.read_versioned(key)
    if existing is not None:
        previous = json.loads(existing)
        old, new = version_key(previous["version"]), version_key(manifest["version"])
        if old > new:
            print(f"Keep newer {channel} version {previous['version']}")
            return
        if old == new and existing != raw_manifest:
            raise ValueError(f"Channel already has a different build with equal version precedence: {key}")
    # A channel is one object: compare-and-swap is enough, without a persistent
    # lock that could be abandoned when a CI runner dies.
    condition = {"IfMatch": etag} if etag is not None else {"IfNoneMatch": "*"}
    try:
        storage.client.put_object(
            Bucket=storage.bucket, Key=key, Body=raw_manifest,
            ContentType="application/json", CacheControl=CHANNEL, **condition,
        )
    except ClientError as error:
        if storage.conflict(error):
            raise ValueError(f"Channel changed concurrently; retry publication: {key}") from None
        raise
    print(f"Promoted {channel} to {manifest['version']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--keyring", type=Path)
    parser.add_argument("--channel", required=True, choices=("dev", "stable"))
    parser.add_argument("--promote", action="store_true")
    args = parser.parse_args()
    raw_manifest = (args.input / "manifest.json").read_bytes()
    manifest = json.loads(raw_manifest)
    if manifest["schemaVersion"] != 1 or manifest["product"] != "hrc":
        raise ValueError("Unsupported build manifest")
    version_key(manifest["version"])
    if not re.fullmatch(r"[0-9a-fA-F]{40}", manifest["commit"]):
        raise ValueError("Manifest must contain the full source commit")
    storage = Storage()
    if args.promote:
        promote(storage, args.channel, manifest, raw_manifest)
    else:
        if args.channel == "dev":
            existing = storage.read("hrc/channels/dev.json")
            if existing is not None:
                old, new = version_key(json.loads(existing)["version"]), version_key(manifest["version"])
                if old > new or (old == new and existing != raw_manifest):
                    raise ValueError("Refusing to replace a newer or conflicting dev publication")
        publish(storage, args.input, manifest, args.keyring)


if __name__ == "__main__":
    try:
        main()
    except (ValueError, KeyError, BotoCoreError, ClientError, OSError, subprocess.CalledProcessError) as error:
        print(f"publish: {error}", file=sys.stderr)
        sys.exit(1)

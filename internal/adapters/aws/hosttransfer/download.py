"""Root-only host capability downloads. Errors deliberately omit bearer URLs."""
import base64
import datetime
import hashlib
import json
import os
import re
import secrets
import stat
import tempfile
import time
import urllib.parse
import urllib.request

MAX_MANIFEST = 4 * 1024 * 1024


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError("redirect refused")


client = urllib.request.build_opener(NoRedirect())


def instant(value):
    result = datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("timestamp lacks timezone")
    return result.timestamp()


def valid_url(value):
    parsed = urllib.parse.urlsplit(value)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.fragment:
        raise ValueError("invalid transport")


def download(url, maximum, checksum, target, expires, transfer_seconds=120):
    valid_url(url)
    if maximum < 1 or time.time() >= instant(expires):
        raise ValueError("expired or invalid download")
    expected = base64.b64decode(checksum, validate=True) if checksum else None
    if expected is not None and len(expected) != 32:
        raise ValueError("invalid checksum")
    pending = None
    started = time.monotonic()
    try:
        with client.open(urllib.request.Request(url, method="GET"), timeout=30) as response:
            length = response.headers.get("Content-Length")
            if length is not None and (int(length) < 0 or int(length) > maximum):
                raise ValueError("download exceeds bound")
            with tempfile.NamedTemporaryFile(dir=os.path.dirname(target), delete=False) as output:
                pending = output.name
                os.chmod(pending, 0o600)
                size, digest = 0, hashlib.sha256()
                while True:
                    if time.monotonic() - started > transfer_seconds:
                        raise ValueError("download deadline exceeded")
                    block = response.read(min(65536, maximum - size + 1))
                    if not block:
                        break
                    size += len(block)
                    if size > maximum:
                        raise ValueError("download exceeds bound")
                    digest.update(block)
                    output.write(block)
                if expected is not None and digest.digest() != expected:
                    raise ValueError("download checksum mismatch")
                if length is not None and size != int(length):
                    raise ValueError("download length mismatch")
                if time.time() >= instant(expires):
                    raise ValueError("download capability expired")
                output.flush()
                os.fsync(output.fileno())
        os.replace(pending, target)
        pending = None
        return size
    finally:
        if pending is not None:
            os.unlink(pending)


def install_locked(reference, directory):
    if reference["schema_version"] != 1 or not 0 < reference["size_bytes"] <= MAX_MANIFEST or reference["generation"] < 1:
        raise ValueError("invalid reference")
    os.makedirs(directory, mode=0o700, exist_ok=True)
    if os.path.islink(directory):
        raise ValueError("unsafe directory")
    os.chmod(directory, 0o700)
    pending = os.path.join(directory, "pending.json")
    try:
        size = download(reference["url"], reference["size_bytes"], reference["sha256"], pending, reference["expires_at"])
        if size != reference["size_bytes"]:
            raise ValueError("manifest length mismatch")
        with open(pending, "rb") as source:
            manifest = json.load(source)
        actual, expected = dict(manifest["scope"]), dict(reference["scope"])
        if instant(actual.pop("deadline_at")) != instant(expected.pop("deadline_at")) or actual != expected or manifest["schema_version"] != 1:
            raise ValueError("manifest scope mismatch")
        manifest["generation"] = reference["generation"]
        current = os.path.join(directory, "manifest.json")
        if os.path.exists(current):
            with open(current, "rb") as source:
                previous = json.load(source)
            if previous["scope"] != manifest["scope"] or previous["generation"] > manifest["generation"]:
                raise ValueError("stale or unrelated generation")
        with open(pending, "w", encoding="utf-8") as output:
            json.dump(manifest, output)
        os.replace(pending, os.path.join(directory, "manifest.json"))
    finally:
        if os.path.exists(pending):
            os.unlink(pending)


def install(reference, directory):
    os.makedirs(directory, mode=0o700, exist_ok=True)
    if os.path.islink(directory):
        raise ValueError("unsafe directory")
    with open(os.path.join(directory, ".lock"), "a+b") as lock:
        if os.name != "nt":  # Production hosts are the approved Ubuntu image.
            import fcntl
            fcntl.flock(lock, fcntl.LOCK_EX)
        install_locked(reference, directory)


def read_capability(manifest, key):
    matches = [entry for entry in manifest["objects"] if entry["object"]["key"] == key and entry["method"] == "GET"]
    if len(matches) != 1 or manifest["schema_version"] != 1 or time.time() >= instant(manifest["scope"]["deadline_at"]):
        raise ValueError("object unavailable")
    capability = matches[0]
    if capability.get("headers") or capability.get("form"):
        raise ValueError("unsupported read transport")
    return capability


def fetch(manifest_path, key, target):
    with open(manifest_path, "rb") as source:
        manifest = json.load(source)
    capability = read_capability(manifest, key)
    try:
        return download_capability(manifest, capability, target)
    except Exception:
        # Only an expired lifecycle transport can wait for trusted observation
        # renewal. Bad bytes/unexpired denial and short inline reads fail at once.
        if "generation" not in manifest or time.time() < instant(capability["expires_at"]):
            raise
    current = renewed_capability(manifest_path, manifest, capability, lambda updated: read_capability(updated, key))
    return download_capability(manifest, current, target)


def download_capability(manifest, capability, target):
    obj = capability["object"]
    expires = min(instant(capability["expires_at"]), instant(manifest["scope"]["deadline_at"]))
    budget = 120
    if obj.get("purpose") == "restore_archive":
        expected_prefix = "sessions/" + manifest["scope"]["session_id"] + "/archives/"
        if (obj.get("slot") != "restore-archive" or not obj["key"].startswith(expected_prefix)
                or ".." in obj["key"].split("/") or obj.get("content_type") != "application/gzip"
                or not 0 < obj["max_bytes"] <= 4 * 1024 * 1024 * 1024
                or not obj.get("sha256") or not obj.get("version_id") or obj["version_id"] == "null"):
            raise ValueError("invalid archive read")
        budget = 900
    deadline = datetime.datetime.fromtimestamp(expires, datetime.timezone.utc).isoformat()
    return download(capability["url"], obj["max_bytes"], obj.get("sha256", ""), target, deadline,
                    min(budget, max(0, expires - time.time())))


def renewed_capability(manifest_path, manifest, capability, select):
    started = time.monotonic()
    while time.monotonic() - started < 180 and time.time() < instant(manifest["scope"]["deadline_at"]):
        with open(manifest_path, "rb") as source:
            refreshed = json.load(source)
        if refreshed["scope"] != manifest["scope"]:
            raise ValueError("renewal scope changed")
        if refreshed.get("generation", 0) > manifest["generation"]:
            current = select(refreshed)
            if current["object"] != capability["object"]:
                raise ValueError("renewal object changed")
            if time.time() < instant(current["expires_at"]):
                return current
        time.sleep(1)
    raise ValueError("renewal unavailable before deadline")


def environment(manifest_path, target):
    with open(manifest_path, "rb") as source:
        manifest = json.load(source)
    if manifest["schema_version"] != 1 or time.time() >= instant(manifest["scope"]["deadline_at"]):
        raise ValueError("environment unavailable")
    exports = []
    for name, value in sorted(manifest.get("environment", {}).items()):
        if not re.fullmatch(r"[A-Z][A-Z0-9_]*_B64", name) or not isinstance(value, str):
            raise ValueError("environment invalid")
        exports.append("export " + name + "='" + base64.b64encode(value.encode()).decode() + "'\n")
    pending = None
    try:
        with tempfile.NamedTemporaryFile(dir=os.path.dirname(target), delete=False) as output:
            pending = output.name
            os.chmod(pending, 0o600)
            output.write("".join(exports).encode())
        os.replace(pending, target)
        pending = None
    finally:
        if pending is not None:
            os.unlink(pending)


def upload(manifest_path, slot, source_path):
    with open(manifest_path, "rb") as source:
        manifest = json.load(source)
    capability = upload_capability(manifest, slot)
    try:
        return post_upload(capability, source_path)
    except Exception:
        if "generation" not in manifest or time.time() < instant(capability["expires_at"]):
            raise
    current = renewed_capability(manifest_path, manifest, capability, lambda updated: upload_capability(updated, slot))
    return post_upload(current, source_path)


def upload_capability(manifest, slot):
    matches = [entry for entry in manifest["objects"] if entry["object"]["slot"] == slot and entry["method"] == "POST"]
    if len(matches) != 1 or manifest["schema_version"] != 1 or time.time() >= instant(manifest["scope"]["deadline_at"]):
        raise ValueError("upload unavailable")
    capability, scope = matches[0], manifest["scope"]
    obj, form = capability["object"], capability["form"]
    expected = "/".join(["sessions", scope["session_id"], "runtime", "host-access", scope["operation_id"], scope["attempt_id"], slot])
    if obj["key"] != expected or form.get("key") != expected or form.get("Content-Type") != obj["content_type"] or capability.get("headers"):
        raise ValueError("upload constraints changed")
    valid_url(capability["url"])
    return capability


def post_upload(capability, source_path):
    obj, form = capability["object"], capability["form"]
    if time.time() >= instant(capability["expires_at"]):
        raise ValueError("upload expired")
    boundary = "gsp-" + secrets.token_hex(24)
    prefix = bytearray()
    for name, value in form.items():
        if not re.fullmatch(r"[A-Za-z0-9_-]+", name) or not isinstance(value, str) or len(value) > 16384 or "\r" in value or "\n" in value:
            raise ValueError("upload form invalid")
        prefix.extend(('--' + boundary + '\r\nContent-Disposition: form-data; name="' + name + '"\r\n\r\n' + value + '\r\n').encode())
    prefix.extend(('--' + boundary + '\r\nContent-Disposition: form-data; name="file"; filename="payload"\r\nContent-Type: ' + obj["content_type"] + '\r\n\r\n').encode())
    suffix = ('\r\n--' + boundary + '--\r\n').encode()
    descriptor = os.open(source_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as payload:
        metadata = os.fstat(payload.fileno())
        size = metadata.st_size
        if not stat.S_ISREG(metadata.st_mode) or not obj["min_bytes"] <= size <= obj["max_bytes"]:
            raise ValueError("upload size invalid")
        started = time.monotonic()
        def body():
            yield bytes(prefix)
            remaining = size
            while remaining:
                if time.monotonic() - started > 120 or time.time() >= instant(capability["expires_at"]):
                    raise ValueError("upload deadline exceeded")
                block = payload.read(min(65536, remaining))
                if not block:
                    raise ValueError("upload source truncated")
                remaining -= len(block)
                yield block
            if payload.read(1):
                raise ValueError("upload source grew")
            yield suffix
        request = urllib.request.Request(capability["url"], data=body(), method="POST", headers={"Content-Type": "multipart/form-data; boundary=" + boundary, "Content-Length": str(len(prefix) + size + len(suffix))})
        with client.open(request, timeout=30) as response:
            if response.status not in [200, 201, 204]:
                raise ValueError("upload rejected")


def archive_upload(manifest_path, source_path):
    with open(manifest_path, "rb") as source:
        manifest = json.load(source)
    def select(updated):
        matches = [entry for entry in updated["objects"] if entry["method"] == "PUT" and entry["object"].get("purpose") == "archive_upload"]
        if len(matches) != 1:
            raise ValueError("archive upload unavailable")
        return matches[0]
    capability = select(manifest)
    try:
        return archive_upload_once(manifest, source_path)
    except Exception:
        if "generation" not in manifest or time.time() < instant(capability["expires_at"]):
            raise
    current = renewed_capability(manifest_path, manifest, capability, select)
    return archive_upload_once(dict(manifest, objects=[current]), source_path)


def archive_upload_once(manifest, source_path):
    scope = manifest["scope"]
    matches = [entry for entry in manifest["objects"] if entry["method"] == "PUT" and entry["object"].get("purpose") == "archive_upload"]
    if manifest["schema_version"] != 1 or len(matches) != 1:
        raise ValueError("archive upload unavailable")
    capability = matches[0]
    obj, headers = capability["object"], capability.get("headers", {})
    expected_key = "/".join(["sessions", scope["session_id"], "archives", scope["operation_id"], "session.tar.gz"])
    size = obj["max_bytes"]
    checksum = base64.b64decode(obj["sha256"], validate=True)
    if (obj["key"] != expected_key or obj.get("slot") != "archive" or obj.get("content_type") != "application/gzip"
            or not 0 < size <= 4 * 1024 * 1024 * 1024 or obj.get("min_bytes") != size
            or obj.get("version_id") or len(checksum) != 32 or base64.b64encode(checksum).decode() != obj["sha256"]
            or capability.get("form") or headers != {"Content-Length": str(size), "Content-Type": "application/gzip",
                                                    "If-None-Match": "*", "X-Amz-Checksum-Sha256": obj["sha256"]}):
        raise ValueError("archive upload constraints changed")
    valid_url(capability["url"])
    expires = min(instant(capability["expires_at"]), instant(scope["deadline_at"]))
    started = time.monotonic()
    def check_deadline():
        if time.time() >= expires or time.monotonic() - started > 900:
            raise ValueError("archive upload deadline exceeded")
    check_deadline()
    descriptor = os.open(source_path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as payload:
        metadata = os.fstat(payload.fileno())
        if not stat.S_ISREG(metadata.st_mode) or metadata.st_size != size:
            raise ValueError("archive upload source invalid")
        digest = hashlib.sha256()
        hashed = 0
        while True:
            check_deadline()
            block = payload.read(65536)
            if not block:
                break
            hashed += len(block)
            if hashed > size:
                raise ValueError("archive upload source grew")
            digest.update(block)
        if hashed != size or digest.digest() != checksum:
            raise ValueError("archive upload checksum changed")
        payload.seek(0)
        def body():
            remaining = size
            while remaining:
                check_deadline()
                block = payload.read(min(65536, remaining))
                if not block:
                    raise ValueError("archive upload source truncated")
                remaining -= len(block)
                yield block
            if payload.read(1):
                raise ValueError("archive upload source grew")
        request = urllib.request.Request(capability["url"], data=body(), method="PUT", headers=headers)
        with client.open(request, timeout=30) as response:
            if response.status not in [200, 201, 204]:
                raise ValueError("archive upload rejected")


def inline(manifest, directory):
    if manifest["schema_version"] != 1 or len(manifest["objects"]) != 1 or time.time() >= instant(manifest["scope"]["deadline_at"]):
        raise ValueError("inline manifest invalid")
    os.makedirs(directory, mode=0o700, exist_ok=True)
    if os.path.islink(directory):
        raise ValueError("unsafe directory")
    os.chmod(directory, 0o700)
    pending = None
    try:
        with tempfile.NamedTemporaryFile(dir=directory, delete=False) as output:
            pending = output.name
            os.chmod(pending, 0o600)
            output.write(json.dumps(manifest).encode())
        os.replace(pending, os.path.join(directory, "manifest.json"))
        pending = None
    finally:
        if pending is not None:
            os.unlink(pending)


if __name__ == "__main__":
    import sys
    try:
        if hasattr(os, "geteuid") and os.geteuid() != 0:
            raise ValueError("root required")
        if sys.argv[1] == "install":
            if len(sys.argv[2]) > 8192:
                raise ValueError("reference exceeds bound")
            install(json.loads(sys.argv[2]), sys.argv[3])
        elif sys.argv[1] == "fetch":
            fetch(sys.argv[2], sys.argv[3], sys.argv[4])
        elif sys.argv[1] == "upload":
            upload(sys.argv[2], sys.argv[3], sys.argv[4])
        elif sys.argv[1] == "archive-upload":
            archive_upload(sys.argv[2], sys.argv[3])
        elif sys.argv[1] == "environment":
            environment(sys.argv[2], sys.argv[3])
        elif sys.argv[1] == "inline":
            if len(sys.argv[2]) > 8192:
                raise ValueError("inline manifest exceeds bound")
            inline(json.loads(sys.argv[2]), sys.argv[3])
        else:
            raise ValueError("unknown operation")
    except Exception:
        print("ERR_HOST_ACCESS: bounded capability transfer failed", file=sys.stderr)
        sys.exit(1)

import base64
import hashlib
import io
import json
import os
import tempfile
import unittest
from unittest.mock import patch
import download


class Response(io.BytesIO):
    def __init__(self, payload, length=None):
        super().__init__(payload)
        self.headers = {} if length is None else {"Content-Length": str(length)}


class DownloadTests(unittest.TestCase):
    def test_archive_expiry_retries_only_same_prepared_object(self):
        obj = {"purpose": "archive_upload", "sha256": "prepared", "max_bytes": 42}
        expired = {"method": "PUT", "object": obj, "expires_at": "2000-01-01T00:00:00Z"}
        manifest = {"scope": {"deadline_at": "2099-01-01T00:00:00Z"}, "generation": 1, "objects": [expired]}
        with tempfile.TemporaryDirectory() as directory:
            reference = os.path.join(directory, "manifest.json")
            with open(reference, "w") as output:
                json.dump(manifest, output)
            renewed = dict(expired, expires_at="2099-01-01T00:00:00Z")
            with patch.object(download, "archive_upload_once", side_effect=[ValueError("expired"), None]) as upload, patch.object(download, "renewed_capability", return_value=renewed) as renewal:
                download.archive_upload(reference, "/prepared")
                self.assertEqual(upload.call_count, 2)
                selected = renewal.call_args.args[-1](dict(manifest, objects=[renewed]))
                self.assertEqual(selected["object"], obj)
            with patch.object(download, "archive_upload_once", side_effect=ValueError("bad bytes")), patch.object(download, "renewed_capability") as renewal:
                manifest["objects"] = [renewed]
                with open(reference, "w") as output:
                    json.dump(manifest, output)
                with self.assertRaises(ValueError):
                    download.archive_upload(reference, "/prepared")
                renewal.assert_not_called()

    def test_archive_put_streams_exact_prepared_bytes_and_denies_changed_constraints(self):
        payload = b"archive" * 10000
        checksum = base64.b64encode(hashlib.sha256(payload).digest()).decode()
        obj = {"purpose": "archive_upload", "slot": "archive", "key": "sessions/session/archives/operation/session.tar.gz",
               "content_type": "application/gzip", "min_bytes": len(payload), "max_bytes": len(payload), "sha256": checksum}
        headers = {"Content-Length": str(len(payload)), "Content-Type": "application/gzip", "If-None-Match": "*", "X-Amz-Checksum-Sha256": checksum}
        capability = {"object": obj, "headers": headers, "method": "PUT", "url": "https://assets.test/archive", "expires_at": "2099-01-01T00:00:00Z"}
        manifest = {"schema_version": 1, "scope": {"session_id": "session", "operation_id": "operation", "deadline_at": "2099-01-01T00:00:00Z"}, "objects": [capability]}
        with tempfile.TemporaryDirectory() as directory:
            source, reference = os.path.join(directory, "archive"), os.path.join(directory, "manifest.json")
            with open(source, "wb") as output:
                output.write(payload)
            def save():
                with open(reference, "w") as output:
                    json.dump(manifest, output)
            save()
            def accept(request, timeout):
                self.assertEqual(request.get_method(), "PUT")
                self.assertEqual(request.get_header("If-none-match"), "*")
                chunks = list(request.data)
                self.assertTrue(all(len(chunk) <= 65536 for chunk in chunks))
                self.assertEqual(b"".join(chunks), payload)
                response = Response(b"")
                response.status = 200
                return response
            with patch.object(download.client, "open", side_effect=accept) as opened:
                download.archive_upload(reference, source)
                opened.assert_called_once()
            for field, value in [("key", "sessions/foreign/archives/operation/session.tar.gz"), ("min_bytes", 1), ("sha256", "invalid")]:
                with self.subTest(field=field), patch.object(download.client, "open") as opened:
                    capability["object"] = dict(obj, **{field: value})
                    save()
                    with self.assertRaises(ValueError):
                        download.archive_upload(reference, source)
                    opened.assert_not_called()
            capability["object"] = obj
            save()
            with open(source, "wb") as output:
                output.write(b"x" * len(payload))
            with patch.object(download.client, "open") as opened:
                with self.assertRaises(ValueError):
                    download.archive_upload(reference, source)
                opened.assert_not_called()

    def test_archive_transfer_budget_is_scoped_and_clipped_to_operation(self):
        manifest = {"scope": {"session_id": "session", "deadline_at": "2030-01-01T00:10:00Z"}}
        obj = {"purpose": "restore_archive", "slot": "restore-archive", "key": "sessions/session/archives/previous/session.tar.gz",
               "content_type": "application/gzip", "max_bytes": 4 * 1024 * 1024 * 1024,
               "sha256": base64.b64encode(hashlib.sha256(b"archive").digest()).decode(), "version_id": "pinned"}
        capability = {"object": obj, "url": "https://assets.test/archive", "expires_at": "2030-01-01T00:15:00Z"}
        with patch.object(download.time, "time", return_value=download.instant("2030-01-01T00:00:00Z")), patch.object(download, "download") as transfer:
            download.download_capability(manifest, capability, "/archive")
            self.assertEqual(transfer.call_args.args[-1], 600)
            for field, value in [("key", "sessions/foreign/archives/archive"), ("version_id", "null"),
                                 ("max_bytes", 4 * 1024 * 1024 * 1024 + 1), ("sha256", ""), ("slot", "other")]:
                with self.subTest(field=field):
                    transfer.reset_mock()
                    with self.assertRaises(ValueError):
                        download.download_capability(manifest, dict(capability, object=dict(obj, **{field: value})), "/archive")
                    transfer.assert_not_called()
            download.download_capability(manifest, dict(capability, object=dict(obj, purpose="mission")), "/mission")
            self.assertEqual(transfer.call_args.args[-1], 120)

    def test_expired_upload_renews_only_same_scope_and_object(self):
        scope = {"session_id": "session", "operation_id": "operation", "attempt_id": "attempt", "deadline_at": "2099-01-01T00:00:00Z"}
        key = "sessions/session/runtime/host-access/operation/attempt/mission-42"
        obj = {"slot": "mission-42", "key": key, "content_type": "application/octet-stream", "min_bytes": 16, "max_bytes": 32}
        capability = {"object": obj, "method": "POST", "url": "https://assets.test/", "expires_at": "2000-01-01T00:00:00Z", "form": {"key": key, "Content-Type": obj["content_type"], "policy": "bounded", "X-Amz-Signature": "signature"}}
        original = {"schema_version": 1, "scope": scope, "generation": 1, "objects": [capability]}
        for changed in ["none", "scope", "object"]:
            with tempfile.TemporaryDirectory() as directory:
                source, payload = os.path.join(directory, "manifest.json"), os.path.join(directory, "mission.pbo")
                with open(source, "w") as output:
                    json.dump(original, output)
                with open(payload, "wb") as output:
                    output.write(b"a" * 16)
                updated = dict(capability, expires_at="2099-01-01T00:00:00Z")
                renewed = dict(original, generation=2, objects=[updated])
                if changed == "scope":
                    renewed["scope"] = dict(scope, attempt_id="other")
                if changed == "object":
                    updated["object"] = dict(obj, max_bytes=64)
                def install_generation(_):
                    with open(source, "w") as output:
                        json.dump(renewed, output)
                def accept(request, timeout):
                    list(request.data)
                    response = Response(b"")
                    response.status = 204
                    return response
                with patch.object(download.time, "sleep", side_effect=install_generation), patch.object(download.client, "open", side_effect=accept) as opened:
                    if changed == "none":
                        download.upload(source, "mission-42", payload)
                        opened.assert_called_once()
                    else:
                        with self.assertRaises(ValueError):
                            download.upload(source, "mission-42", payload)
                        opened.assert_not_called()

    def test_upload_streams_exact_slot_and_rejects_invalid_bounds(self):
        scope = {"session_id": "session", "operation_id": "operation", "attempt_id": "attempt", "deadline_at": "2099-01-01T00:00:00Z"}
        key = "sessions/session/runtime/host-access/operation/attempt/mission-42"
        obj = {"slot": "mission-42", "key": key, "content_type": "application/octet-stream", "min_bytes": 16, "max_bytes": 32}
        capability = {"object": obj, "method": "POST", "url": "https://assets.test/", "expires_at": "2099-01-01T00:00:00Z", "form": {"key": key, "Content-Type": obj["content_type"], "policy": "bounded", "X-Amz-Signature": "signature"}}
        manifest = {"schema_version": 1, "scope": scope, "objects": [capability]}
        with tempfile.TemporaryDirectory() as directory:
            source, path = os.path.join(directory, "manifest.json"), os.path.join(directory, "payload.pbo")
            with open(source, "w") as output:
                json.dump(manifest, output)
            with open(path, "wb") as output:
                output.write(b"a" * 16)
            def accept(request, timeout):
                self.assertEqual(request.get_method(), "POST")
                self.assertEqual(timeout, 30)
                chunks = list(request.data)
                body = b"".join(chunks)
                self.assertEqual(int(request.get_header("Content-length")), len(body))
                self.assertIn(key.encode(), body)
                self.assertIn(b"a" * 16, body)
                response = Response(b"")
                response.status = 204
                return response
            with patch.object(download.client, "open", side_effect=accept) as opened:
                download.upload(source, "mission-42", path)
                opened.assert_called_once()
            with patch.object(download.client, "open") as opened:
                with self.assertRaises(ValueError):
                    download.upload(source, "mission-43", path)
                with open(path, "wb") as output:
                    output.write(b"a" * 33)
                with self.assertRaises(ValueError):
                    download.upload(source, "mission-42", path)
                opened.assert_not_called()
                manifest["objects"][0]["form"]["key"] = "sessions/other/input/mission.pbo"
                with open(source, "w") as output:
                    json.dump(manifest, output)
                with self.assertRaises(ValueError):
                    download.upload(source, "mission-42", path)
                opened.assert_not_called()

    def test_expired_read_retries_only_new_generation_same_object(self):
        payload = b"accepted mission"
        digest = base64.b64encode(hashlib.sha256(payload).digest()).decode()
        capability = {"object": {"key": "accepted", "max_bytes": len(payload), "sha256": digest, "version_id": "pinned"}, "method": "GET", "url": "https://assets.test/mission", "expires_at": "2000-01-01T00:00:00Z"}
        original = {"schema_version": 1, "scope": {"deadline_at": "2099-01-01T00:00:00Z"}, "generation": 1, "objects": [capability]}
        for changed in [False, True]:
            with tempfile.TemporaryDirectory() as directory:
                source, target = os.path.join(directory, "manifest.json"), os.path.join(directory, "mission.pbo")
                with open(source, "w") as output:
                    json.dump(original, output)
                updated = dict(capability, expires_at="2099-01-01T00:00:00Z")
                if changed:
                    updated["object"] = dict(capability["object"], version_id="different")
                renewed = dict(original, generation=2, objects=[updated])
                def install_generation(_):
                    with open(source, "w") as output:
                        json.dump(renewed, output)
                with patch.object(download.time, "sleep", side_effect=install_generation), patch.object(download.client, "open", return_value=Response(payload)) as opened:
                    if changed:
                        with self.assertRaises(ValueError):
                            download.fetch(source, "accepted", target)
                        opened.assert_not_called()
                    else:
                        download.fetch(source, "accepted", target)
                        opened.assert_called_once()
                        with open(target, "rb") as result:
                            self.assertEqual(result.read(), payload)

    def test_inline_mission_fetch_uses_exact_inventory(self):
        payload = b"accepted mission"
        digest = base64.b64encode(hashlib.sha256(payload).digest()).decode()
        capability = {"object": {"key": "accepted-mission", "max_bytes": len(payload), "sha256": digest}, "method": "GET", "url": "https://assets.test/mission", "expires_at": "2099-01-01T00:00:00Z"}
        manifest = {"schema_version": 1, "scope": {"deadline_at": "2099-01-01T00:00:00Z"}, "objects": [capability]}
        with tempfile.TemporaryDirectory() as directory:
            download.inline(manifest, directory)
            source, target = os.path.join(directory, "manifest.json"), os.path.join(directory, "mission.pbo")
            with patch.object(download.client, "open", return_value=Response(payload)) as opened:
                with self.assertRaises(ValueError):
                    download.fetch(source, "unaccepted-mission", target)
                opened.assert_not_called()
                download.fetch(source, "accepted-mission", target)
            with open(target, "rb") as result:
                self.assertEqual(result.read(), payload)
            with self.assertRaises(ValueError):
                download.inline(dict(manifest, objects=[capability, capability]), directory)

    def test_environment_exports_are_encoded_and_names_are_checked(self):
        with tempfile.TemporaryDirectory() as directory:
            source, target = os.path.join(directory, "manifest.json"), os.path.join(directory, "environment.sh")
            value = "mission'; touch /tmp/injected\n$(secret)"
            manifest = {"schema_version": 1, "scope": {"deadline_at": "2099-01-01T00:00:00Z"}, "environment": {"MISSION_MANIFEST_B64": value}}
            with open(source, "w") as output:
                json.dump(manifest, output)
            download.environment(source, target)
            with open(target) as output:
                self.assertEqual(output.read(), "export MISSION_MANIFEST_B64='" + base64.b64encode(value.encode()).decode() + "'\n")
            manifest["environment"] = {"BASH_ENV": value}
            with open(source, "w") as output:
                json.dump(manifest, output)
            with self.assertRaises(ValueError):
                download.environment(source, target)

    def test_atomic_bounds_checksum_and_redirection(self):
        payload = b"accepted mission"
        digest = base64.b64encode(hashlib.sha256(payload).digest()).decode()
        with tempfile.TemporaryDirectory() as directory:
            target = os.path.join(directory, "mission.pbo")
            with open(target, "wb") as output:
                output.write(b"previous")
            for maximum, checksum, length in [(3, digest, None), (100, base64.b64encode(bytes(32)).decode(), None), (100, digest, 99)]:
                with patch.object(download.client, "open", return_value=Response(payload, length)):
                    with self.assertRaises(ValueError):
                        download.download("https://assets.test/object?secret=bearer", maximum, checksum, target, "2099-01-01T00:00:00Z")
                with open(target, "rb") as source:
                    self.assertEqual(source.read(), b"previous")
                self.assertEqual(os.listdir(directory), ["mission.pbo"])
            with patch.object(download.client, "open", return_value=Response(payload, len(payload))):
                download.download("https://assets.test/object", len(payload), digest, target, "2099-01-01T00:00:00Z")
            with open(target, "rb") as source:
                self.assertEqual(source.read(), payload)
            with self.assertRaises(ValueError):
                download.NoRedirect().redirect_request(None, None, None, None, None, None)
            with self.assertRaises(ValueError):
                download.download("https://assets.test/object", 100, digest, target, "2000-01-01T00:00:00Z")

    def test_manifest_integrity_scope_and_generation(self):
        scope = {"session_id": "session", "deadline_at": "2099-01-01T00:00:00Z"}
        manifest = {"schema_version": 1, "scope": scope, "objects": []}
        payload = json.dumps(manifest).encode()
        reference = {"schema_version": 1, "scope": scope, "generation": 2, "size_bytes": len(payload), "sha256": base64.b64encode(hashlib.sha256(payload).digest()).decode(), "url": "https://assets.test/manifest", "expires_at": "2099-01-01T00:00:00Z"}
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(download.client, "open", return_value=Response(payload)):
                download.install(reference, directory)
            stale = dict(reference, generation=1)
            with patch.object(download.client, "open", return_value=Response(payload)):
                with self.assertRaises(ValueError):
                    download.install(stale, directory)
            wrong = dict(reference, scope=dict(scope, session_id="other"))
            with patch.object(download.client, "open", return_value=Response(payload)):
                with self.assertRaises(ValueError):
                    download.install(wrong, directory)
            with open(os.path.join(directory, "manifest.json")) as source:
                self.assertEqual(json.load(source)["generation"], 2)
            self.assertFalse(os.path.exists(os.path.join(directory, "pending.json")))


if __name__ == "__main__":
    unittest.main()

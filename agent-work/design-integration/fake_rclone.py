"""Fake rclone rc server over a unix socket for local UI integration tests.

Implements the allowlisted methods Cloud Ferry uses:
core/version, operations/about|list|stat|size|check, config/create|get|delete,
sync/copy, sync/sync, job/status, job/stop, core/stats.
"""
import json
import os
import socket
import socketserver
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SOCKET = os.environ.get("FAKE_RC_SOCKET", "/tmp/cf-test/rclone.sock")

ABOUT = {"total": 214748364800, "used": 138000000000, "free": 76748364800}

# Cloud tree: dir -> dict(dirs=[...], files=[(name,size,modified)])
CLOUD = {
    "": {
        "dirs": ["Фотографии", "Документы", "Видео", "Музыка"],
        "files": [("readme.txt", 1234, "2026-11-10T12:00:00Z")],
    },
    "Фотографии": {
        "dirs": ["2024", "2023"],
        "files": [("cover.jpg", 5242880, "2026-11-01T10:00:00Z")],
    },
    "Фотографии/2024": {
        "dirs": [],
        "files": [("IMG_001.jpg", 4194304, "2026-10-02T09:00:00Z"),
                  ("IMG_002.jpg", 8388608, "2026-10-03T09:00:00Z")],
    },
    "Фотографии/2023": {"dirs": [], "files": [("IMG_100.jpg", 2097152, "2026-09-01T09:00:00Z")]},
    "Документы": {
        "dirs": ["Работа", "Личное"],
        "files": [("passport.pdf", 1048576, "2026-08-01T09:00:00Z")],
    },
    "Документы/Работа": {"dirs": [], "files": [("plan.docx", 512000, "2026-08-02T09:00:00Z")]},
    "Документы/Личное": {"dirs": [], "files": []},
    "Видео": {"dirs": [], "files": [("movie.mp4", 1073741824, "2026-07-01T09:00:00Z")]},
    "Музыка": {"dirs": [], "files": []},
}

REMOTES = {}
LOCK = threading.Lock()
JOB_SEQ = [100]
JOBS = {}


def cloud_node(path):
    path = path.strip("/")
    return CLOUD.get(path)


def size_of(path):
    node = cloud_node(path)
    if node is None:
        return None
    count, total = len(node["files"]), sum(f[1] for f in node["files"])
    for child in node["dirs"]:
        sub = size_of(path + "/" + child if path else child)
        if sub is not None:
            count += sub[0]
            total += sub[1]
    return count, total


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, obj):
        body = json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw or b"{}")
        except Exception:
            body = {}
        method = self.path.strip("/")
        if method.startswith("rc/"):
            method = method[3:]
        with LOCK:
            getattr(self, "m_" + method.replace("/", "_"), self.m_unknown)(body)

    def m_unknown(self, body):
        self._send(404, {"error": "fake rclone: method not implemented"})

    def m_core_version(self, body):
        self._send(200, {"version": "v1.69.0", "decomposed": [1, 69, 0],
                         "isGit": False, "isBeta": False})

    def m_operations_about(self, body):
        self._send(200, dict(ABOUT))

    def m_operations_list(self, body):
        fs = str(body.get("fs") or "")
        remote = str(body.get("remote") or "").strip("/")
        path = (fs.split(":", 1)[1] if ":" in fs else "").strip("/")
        full = "/".join(p for p in (path, remote) if p)
        node = cloud_node(full)
        if node is None:
            self._send(404, {"error": "directory not found"})
            return
        dirs_only = bool((body.get("opt") or {}).get("dirsOnly"))
        items = []
        for name in node["dirs"]:
            items.append({"Path": name, "Name": name, "Size": -1, "MimeType": "inode/directory",
                          "ModTime": "2026-10-01T09:00:00Z", "IsDir": True})
        if not dirs_only:
            for name, size, modified in node["files"]:
                items.append({"Path": name, "Name": name, "Size": size,
                              "MimeType": "application/octet-stream",
                              "ModTime": modified, "IsDir": False})
        self._send(200, {"list": items})

    def m_operations_stat(self, body):
        fs = str(body.get("fs") or "")
        remote = str(body.get("remote") or "").strip("/")
        path = (fs.split(":", 1)[1] if ":" in fs else "").strip("/")
        full = "/".join(p for p in (path, remote) if p)
        node = cloud_node(full)
        if node is not None:
            self._send(200, {"item": {"Path": full, "Name": full.split("/")[-1], "Size": -1,
                                      "MimeType": "inode/directory",
                                      "ModTime": "2026-10-01T09:00:00Z", "IsDir": True}})
            return
        parent, name = (full.rsplit("/", 1) if "/" in full else ("", full))
        parent_node = cloud_node(parent)
        if parent_node:
            for fname, fsize, modified in parent_node["files"]:
                if fname == name:
                    self._send(200, {"item": {"Path": full, "Name": name, "Size": fsize,
                                              "MimeType": "application/octet-stream",
                                              "ModTime": modified, "IsDir": False}})
                    return
        self._send(404, {"error": "object not found"})

    def m_operations_size(self, body):
        fs = str(body.get("fs") or "")
        path = (fs.split(":", 1)[1] if ":" in fs else "").strip("/")
        size = size_of(path)
        if size is None:
            self._send(404, {"error": "directory not found"})
            return
        self._send(200, {"count": size[0], "bytes": size[1]})

    def m_operations_check(self, body):
        self._send(200, {"hashType": "md5", "matched": 3, "differ": 0,
                         "missingOnDst": [], "error": []})

    def m_config_create(self, body):
        name = str(body.get("name") or "")
        if not name:
            self._send(400, {"error": "name is required"})
            return
        REMOTES[name] = {"type": body.get("type"), "parameters": body.get("parameters") or {}}
        self._send(200, {})

    def m_config_get(self, body):
        name = str(body.get("name") or "")
        remote = REMOTES.get(name)
        if remote is None:
            self._send(404, {"error": "remote not found"})
            return
        params = dict(remote.get("parameters") or {})
        self._send(200, params)

    def m_config_delete(self, body):
        REMOTES.pop(str(body.get("name") or ""), None)
        self._send(200, {})

    def _start_job(self, body):
        jobid = JOB_SEQ[0]
        JOB_SEQ[0] += 1
        group = str(body.get("_group") or "job-%d" % jobid)
        interval = float(os.environ.get("FAKE_JOB_FINISH_AFTER", "0") or 0)
        JOBS[jobid] = {
            "group": group,
            "started": time.time(),
            "executeId": "exec-%d" % jobid,
            "finish_after": interval,
            "bytes": 0,
            "total": 250000000,
        }
        self._send(200, {"jobid": jobid, "executeId": "exec-%d" % jobid})

    def m_sync_copy(self, body):
        self._start_job(body)

    def m_sync_sync(self, body):
        self._start_job(body)

    def m_job_status(self, body):
        jobid = int(body.get("jobid") or 0)
        job = JOBS.get(jobid)
        if job is None:
            self._send(404, {"error": "job not found"})
            return
        elapsed = time.time() - job["started"]
        finished = job["finish_after"] > 0 and elapsed >= job["finish_after"]
        self._send(200, {"finished": finished, "success": finished,
                         "error": "" if finished else "", "executeId": job["executeId"]})

    def m_job_stop(self, body):
        jobid = int(body.get("jobid") or 0)
        JOBS.pop(jobid, None)
        self._send(200, {})

    def m_core_stats(self, body):
        group = str(body.get("group") or "")
        job = next((j for j in JOBS.values() if j["group"] == group), None)
        if job is None:
            self._send(200, {"bytes": 0, "totalBytes": 0, "speed": 0, "eta": 0,
                             "transfers": 0, "totalTransfers": 0, "errors": 0,
                             "elapsedTime": 0})
            return
        job["bytes"] = min(job["total"], job["bytes"] + 12500000)
        self._send(200, {"bytes": job["bytes"], "totalBytes": job["total"],
                         "speed": 8808038, "eta": max(0, int((job["total"] - job["bytes"]) / 8808038)),
                         "transfers": job["bytes"] // 2500000, "totalTransfers": 100,
                         "errors": 0, "elapsedTime": time.time() - job["started"]})


class UnixHTTPServer(ThreadingHTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        socketserver.UnixStreamServer.server_bind(self)
        self.server_name = "rclone"
        self.server_port = 0

    def get_request(self):
        request, _ = self.socket.accept()
        return request, ("127.0.0.1", 0)


def main():
    try:
        os.unlink(SOCKET)
    except FileNotFoundError:
        pass
    os.makedirs(os.path.dirname(SOCKET), exist_ok=True)
    for spec in filter(None, os.environ.get("FAKE_SEED_JOBS", "").split(",")):
        parts = spec.split(":")
        jobid = int(parts[0])
        JOBS[jobid] = {"group": parts[1], "started": time.time(),
                       "executeId": parts[2] if len(parts) > 2 else "exec-%d" % jobid,
                       "finish_after": 0, "bytes": 4200000000, "total": 12000000000}
    server = UnixHTTPServer(SOCKET, Handler)
    print("fake rclone rc on", SOCKET, flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()

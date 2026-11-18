import json
import os
import shutil

ROOT = "/tmp/cf-test"
shutil.rmtree(os.path.join(ROOT, "data"), ignore_errors=True)
shutil.rmtree(os.path.join(ROOT, "downloads"), ignore_errors=True)
os.makedirs(os.path.join(ROOT, "data"), exist_ok=True)

downloads = os.path.join(ROOT, "downloads")
for rel in ["", "yandex", "yandex/2024", "Архив", "Фотографии"]:
    os.makedirs(os.path.join(downloads, rel), exist_ok=True)
with open(os.path.join(downloads, "yandex", "already.txt"), "w") as f:
    f.write("x")

state = {
    "accounts": [
        {
            "id": "a1",
            "name": "artem",
            "provider": "webdav",
            "login": "artem",
            "remote": "cf_a1",
            "createdAt": "2026-11-16T10:00:00Z",
            "status": "ok",
            "checkedAt": "2026-11-18T16:00:00Z",
        }
    ],
    "jobs": [
        {
            "id": "j1",
            "accountId": "a1",
            "source": "cf_a1:",
            "sourcePaths": ["Фотографии"],
            "dest": "/tmp/cf-test/downloads/yandex",
            "mode": "copy",
            "status": "running",
            "group": "g1",
            "rcloneJobId": 7,
            "executeId": "exec-7",
            "stats": {
                "bytes": 4200000000,
                "totalBytes": 12000000000,
                "speed": 8808038,
                "eta": 900,
                "transfers": 40,
                "totalTransfers": 100,
                "errors": 0,
                "elapsedTime": 120,
            },
            "createdAt": "2026-11-18T15:00:00Z",
            "startedAt": "2026-11-18T15:00:10Z",
        },
        {
            "id": "j2",
            "accountId": "a1",
            "source": "cf_a1:Документы",
            "sourcePaths": ["Документы"],
            "dest": "/tmp/cf-test/downloads/Архив",
            "mode": "copy",
            "status": "done",
            "group": "g2",
            "rcloneJobId": 8,
            "executeId": "exec-8",
            "stats": {
                "bytes": 3980000000,
                "totalBytes": 3980000000,
                "speed": 0,
                "eta": 0,
                "transfers": 812,
                "totalTransfers": 812,
                "errors": 0,
                "elapsedTime": 340,
            },
            "createdAt": "2026-11-17T10:00:00Z",
            "startedAt": "2026-11-17T10:00:05Z",
            "finishedAt": "2026-11-17T10:06:00Z",
        },
        {
            "id": "j3",
            "accountId": "a1",
            "source": "cf_a1:Видео",
            "sourcePaths": ["Видео"],
            "dest": "/tmp/cf-test/downloads/yandex/2024",
            "mode": "copy",
            "status": "failed",
            "group": "g3",
            "rcloneJobId": 9,
            "executeId": "exec-9",
            "error": "rclone exited: 3 errors",
            "errorTitle": "Не удалось скачать 3 файла",
            "errorAdvice": "Проверьте свободное место и повторите.",
            "failures": ["Видео/a.mp4", "Видео/b.mp4", "Видео/c.mp4"],
            "failureCount": 3,
            "stats": {
                "bytes": 100000000,
                "totalBytes": 800000000,
                "speed": 0,
                "eta": 0,
                "transfers": 1,
                "totalTransfers": 4,
                "errors": 3,
                "elapsedTime": 40,
            },
            "createdAt": "2026-11-18T12:00:00Z",
            "startedAt": "2026-11-18T12:00:05Z",
            "finishedAt": "2026-11-18T12:00:45Z",
        },
    ],
}
with open(os.path.join(ROOT, "data", "state.json"), "w") as f:
    json.dump(state, f, ensure_ascii=False, indent=2)
print("env ready in", ROOT)

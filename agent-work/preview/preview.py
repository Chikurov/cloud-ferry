import json, os, re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse, parse_qs

WEB = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..', 'internal', 'httpapi', 'web'))
CLOUD = {
  '': ['Фотографии', 'Документы', 'Видео'],
  'Фотографии': ['2024', '2023'],
  'Документы': ['Работа', 'Личное'],
  'Видео': [],
  'Фотографии/2024': [], 'Фотографии/2023': [],
  'Документы/Работа': [], 'Документы/Личное': [],
}
SERVER = {
  '/downloads': ['yandex'],
  '/downloads/yandex': [],
}
STATE = {
  'downloads': '/downloads',
  'accounts': [{
    'id': 'a1', 'name': 'artem', 'login': 'artem', 'provider': 'webdav',
    'remote': 'cf_a1', 'status': 'ok', 'createdAt': '2026-11-13T18:00:00Z',
  }],
  'jobs': [{
    'id': 'j1', 'accountId': 'a1', 'source': 'cf_a1:', 'sourcePaths': ['Фотографии'],
    'dest': '/downloads/yandex', 'mode': 'copy', 'status': 'running', 'group': 'g',
    'rcloneJobId': 7, 'executeId': 'e1',
    'stats': {'bytes': 13212057600, 'totalBytes': 31138512896, 'speed': 8808038,
              'eta': 21400, 'transfers': 1204, 'totalTransfers': 12418},
    'createdAt': '2026-11-13T18:10:00Z',
  }],
}
SYSTEM = {
  'downloads': '/downloads',
  'roots': [{'path': '/downloads', 'free': 1000204886016, 'total': 2000204886016}],
  'cloud': {'accountId': 'a1', 'name': 'artem', 'provider': 'webdav', 'status': 'ok',
            'total': 214748364800, 'used': 138000000000, 'free': 76748364800,
            'available': True, 'stale': False, 'updatedAt': '2026-11-15T12:00:00Z'},
}

def folder_payload(path):
    items = CLOUD.get(path)
    if items is None:
        return None
    parent = '' if path == '' else '/'.join(path.split('/')[:-1])
    dirs = [{'name': n, 'path': (path + '/' + n) if path else n} for n in items]
    return {'accountId': 'a1', 'path': path, 'parent': parent, 'dirs': dirs,
            'entries': dirs, 'truncated': False}

class H(BaseHTTPRequestHandler):
    protocol_version = 'HTTP/1.1'
    def log_message(self, *a): pass
    def _send(self, code, obj, ctype='application/json'):
        body = obj if isinstance(obj, bytes) else json.dumps(obj, ensure_ascii=False).encode()
        self.send_response(code)
        self.send_header('Content-Type', ctype + '; charset=utf-8')
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(body)
    def _file(self, name, ctype):
        p = os.path.join(WEB, name)
        if not os.path.isfile(p):
            return self._send(404, {'error': 'not found'})
        with open(p, 'rb') as f:
            self._send(200, f.read(), ctype)
    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        p = u.path
        if p in ('/', '/index.html'):
            return self._file('index.html', 'text/html')
        if p == '/style.css':
            return self._file('style.css', 'text/css')
        if p == '/app.js':
            return self._file('app.js', 'application/javascript')
        if p == '/api/state':
            if os.environ.get('MOCK_EMPTY') == '1':
                empty = dict(STATE); empty['accounts'] = []; empty['jobs'] = []
                return self._send(200, empty)
            return self._send(200, STATE)
        if p == '/api/system':
            return self._send(200, SYSTEM)
        if p in ('/api/healthz', '/api/readyz'):
            return self._send(200, {'ok': True, 'rclone': 'v1.76.0', 'version': 'preview'})
        if p == '/api/oauth/yandex/status':
            return self._send(200, {'configured': False})
        if p == '/api/widget':
            return self._send(200, {'type': 'text-with-progress', 'title': 'Cloud Ferry',
                                    'text': '12,3 ГБ из 29 ГБ', 'progress': 0.42})
        if p in ('/api/fs/folders', '/api/fs/dirs'):
            path = (q.get('path') or ['/downloads'])[0]
            items = SERVER.get(path)
            if items is None:
                return self._send(404, {'error': 'Папка не найдена на сервере.'})
            parent = None if path == '/downloads' else os.path.dirname(path)
            dirs = [{'name': n, 'path': path.rstrip('/') + '/' + n} for n in items]
            return self._send(200, {'root': '/downloads', 'path': path, 'parent': parent,
                                    'dirs': dirs, 'entries': dirs, 'roots': ['/downloads'],
                                    'free': 1000204886016, 'total': 2000204886016,
                                    'writable': True, 'truncated': False})
        m = re.match(r'^/api/accounts/([^/]+)/folders$', p)
        if m:
            path = (q.get('path') or [''])[0]
            data = folder_payload(path)
            if data is None:
                return self._send(404, {'error': 'Папка не найдена в облаке.'})
            return self._send(200, data)
        return self._send(404, {'error': 'not found'})
    def do_POST(self):
        u = urlparse(self.path)
        n = int(self.headers.get('Content-Length') or 0)
        try:
            body = json.loads(self.rfile.read(n) or b'{}')
        except Exception:
            body = {}
        if u.path == '/api/jobs/existing':
            dest = (body.get('destPath') or '/downloads').rstrip('/')
            items = []
            for src in body.get('sourcePaths') or []:
                exists = os.path.isdir(os.path.join('/tmp/preview-nonexistent', src)) or False
                if dest == '/downloads/yandex' and src in ('Документы', 'Видео'):
                    exists = True
                items.append({'path': src, 'exists': exists, 'entries': 12 if exists else 0})
            return self._send(200, {'dest': dest, 'items': items})
        if u.path == '/api/cloud/sizes':
            paths = body.get('paths') or []
            return self._send(200, {'entries': [{'path': x, 'bytes': 524288000, 'files': 120} for x in paths], 'partial': False})
        if u.path.startswith('/api/preflight'):
            return self._send(200, {'files': 12483, 'bytes': 954800000, 'free': 1000204886016, 'fits': True})
        if u.path == '/api/jobs':
            return self._send(201, dict(STATE['jobs'][0], id='j2', status='queued'))
        if u.path.startswith('/api/accounts') and u.path.endswith('/validate'):
            return self._send(200, {'ok': True, 'about': {'total': 214748364800, 'used': 138000000000},
                                    'login': 'artem'})
        if u.path == '/api/accounts':
            return self._send(201, STATE['accounts'][0])
        if re.match(r'^/api/jobs/[^/]+/(stop|resume|check)$', u.path):
            return self._send(200, {'ok': True})
        return self._send(200, {'ok': True})

ThreadingHTTPServer(('127.0.0.1', int(os.environ.get('MOCK_PORT', '8899'))), H).serve_forever()

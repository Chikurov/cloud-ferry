"""Package sources and documentation without generated or machine-local files."""
from pathlib import Path
from zipfile import ZipFile, ZIP_DEFLATED
import os
import tempfile

root = Path(__file__).resolve().parents[1]
package = 'cloud-ferry'
output = root.parent / (package + '-source.zip')
excluded = {'.git', '.gocache', '.gomodcache', '.devdata', 'bin', '__MACOSX',
            '__pycache__', 'node_modules', '.venv', '.release-prep-tmp',
            '.playwright', 'playwright-report', 'test-results', 'ms-playwright',
            'agent-work', '.impeccable'}
fd, temporary = tempfile.mkstemp(prefix='.cloud-ferry-', suffix='.zip', dir=output.parent)
os.close(fd)
try:
    with ZipFile(temporary, 'w', ZIP_DEFLATED, compresslevel=9) as archive:
        for directory, dirs, files in os.walk(root, followlinks=False):
            dirs[:] = sorted(d for d in dirs if d not in excluded and not (Path(directory) / d).is_symlink())
            for name in sorted(files):
                path = Path(directory) / name
                relative = path.relative_to(root)
                local_data = relative.parts[:2] == ('cloudferry-migrator', 'data')
                if path.is_symlink() or name in {'.git', '.DS_Store'} or name.startswith('._'):
                    continue
                if name.endswith(('.zip', '.test', '.pyc', '.log')) or name in {'.DS_Store'} or (name.startswith('.env') and name != '.env.example'):
                    continue
                if local_data and name != '.gitkeep':
                    continue
                archive.write(path, Path(package) / relative)
    os.replace(temporary, output)
finally:
    Path(temporary).unlink(missing_ok=True)
print(f'{output} ({output.stat().st_size / 1_000_000:.1f} MB)')

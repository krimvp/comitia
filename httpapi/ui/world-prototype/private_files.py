"""Read launcher/credential files without following links or exposing shared files."""
import os
import stat
from pathlib import Path


def private_text(path):
    descriptor = os.open(Path(path).expanduser(), os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(descriptor) as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_uid != os.getuid():
            raise ValueError('Expected a private regular file owned by the current user')
        return stream.read()

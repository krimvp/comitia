#!/usr/bin/env python3
"""Open the installed viewer with a private browser-session handoff."""
import argparse
import json
from pathlib import Path
import sys
import urllib.parse
import webbrowser

sys.path.insert(0, str(Path(__file__).resolve().parent))
from private_files import private_text


def launch(root):
    config = json.loads(private_text(root/'service.json'))
    url = config['viewer_url']
    parsed = urllib.parse.urlsplit(url)
    if parsed.scheme != 'http' or parsed.hostname != '127.0.0.1' or not parsed.port or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
        raise ValueError('Expected the installed loopback viewer URL')
    key = private_text(root/'credentials/viewer-launch-key').strip()
    if len(key) < 32:
        raise ValueError('Invalid launcher key')
    webbrowser.open(url + '/#viewer-key=' + urllib.parse.quote(key, safe=''))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path.home()/'Library/Application Support/Comitia')
    launch(parser.parse_args().root)

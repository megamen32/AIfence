"""Read an issued test-user token from the owner's local protected broker."""
import json
import os
from pathlib import Path
import socket
import stat
import struct

SOCKET = Path('/Users/user/AIfence/.runtime/blind-director/login.sock')
ORIGIN = 'http://localhost:42081'


def get_token(case):
    if case not in {'smoke', *(f'case-{i:02d}' for i in range(1, 7))}:
        raise ValueError('Unknown test account')
    parent = SOCKET.parent.stat()
    if parent.st_uid != os.getuid() or parent.st_mode & 0o077:
        raise RuntimeError('Insecure token broker directory')
    info = SOCKET.lstat()
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise RuntimeError('Insecure token broker socket')
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.settimeout(5)
        client.connect(str(SOCKET))
        if not hasattr(socket, 'LOCAL_PEERCRED'):
            raise RuntimeError('Cannot verify token broker peer identity')
        # macOS LOCAL_PEERCRED returns xucred: version followed by effective uid.
        peer = client.getsockopt(0, socket.LOCAL_PEERCRED, 128)
        if len(peer) < 8:
            raise RuntimeError('Invalid token broker peer credentials')
        _, uid = struct.unpack_from('=II', peer)
        if uid != os.getuid():
            raise RuntimeError('Unexpected token broker owner')
        client.sendall((json.dumps({'case':case, 'origin':ORIGIN})+'\n').encode())
        data = b''
        while b'\n' not in data:
            part = client.recv(4096)
            if not part or len(data)+len(part)>16384:
                raise RuntimeError('Invalid token broker reply')
            data += part
    response = json.loads(data.split(b'\n',1)[0])
    token = response.get('access_token')
    if response.get('case') != case or response.get('origin') != ORIGIN or not isinstance(token,str) or not token or '\n' in token:
        raise RuntimeError('Invalid token broker response')
    return token

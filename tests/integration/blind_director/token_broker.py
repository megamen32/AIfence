#!/usr/bin/env python3
"""Serve already-issued stand tokens over a private per-user Unix socket."""
import json
import os
from pathlib import Path
import socket
import stat
import struct

ROOT = Path('/Users/user/AIfence/.runtime/blind-director')
SOCKET = ROOT/'login.sock'
TOKENS = ROOT/'tokens.json'
ORIGIN = 'http://localhost:42081'
CASES = {'smoke', *(f'case-{i:02d}' for i in range(1,7))}


def load_tokens():
    parent=ROOT.stat()
    if parent.st_uid != os.getuid() or parent.st_mode & 0o077:
        raise RuntimeError('Insecure private runtime directory')
    fd=os.open(TOKENS, os.O_RDONLY | getattr(os,'O_NOFOLLOW',0))
    with os.fdopen(fd,'r',encoding='utf-8') as src:
        info=os.fstat(src.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise RuntimeError('Insecure issued-token file')
        tokens=json.load(src)
    if not isinstance(tokens,dict) or not tokens or any(k not in CASES or not isinstance(v,str) or not v or '\n' in v for k,v in tokens.items()):
        raise RuntimeError('Invalid issued-token file')
    return tokens


def response(request, tokens):
    if not isinstance(request,dict) or set(request) != {'case','origin'} or request['origin'] != ORIGIN or request['case'] not in CASES:
        raise ValueError('Invalid token request')
    token=tokens.get(request['case'])
    if not token: raise ValueError('Token unavailable')
    return {'case':request['case'],'origin':ORIGIN,'access_token':token}


def main():
    os.umask(0o077)
    tokens=load_tokens()
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as server:
        server.bind(str(SOCKET))  # never replace an existing broker or socket
        os.chmod(SOCKET,0o600)
        inode=SOCKET.lstat().st_ino
        server.listen(8)
        print('Local test-token broker ready',flush=True)
        try:
            while True:
                conn,_=server.accept()
                with conn:
                    conn.settimeout(5)
                    try:
                        peer=conn.getsockopt(0,socket.LOCAL_PEERCRED,128)
                        if len(peer)<8 or struct.unpack_from('=II',peer)[1] != os.getuid():
                            continue
                        data=b''
                        while b'\n' not in data and len(data)<4096:
                            chunk=conn.recv(4096-len(data))
                            if not chunk: break
                            data+=chunk
                        if b'\n' not in data: continue
                        value=response(json.loads(data.split(b'\n',1)[0]),tokens)
                        conn.sendall((json.dumps(value)+'\n').encode())
                    except (OSError,ValueError,KeyError,TypeError):
                        continue
        finally:
            try:
                if SOCKET.lstat().st_ino == inode: SOCKET.unlink()
            except FileNotFoundError: pass


if __name__ == '__main__': main()

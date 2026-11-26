#!/usr/bin/env python3
"""Save the stand-only operator login in the current Mac user's keychain."""
import ctypes
import json
import os
import shlex
import subprocess
import sys

remote_path = os.environ['AIFENCE_STAND_DATA_ROOT'] + '/operator.json'
remote = 'sudo -n -u aifence-stand cat ' + shlex.quote(remote_path)
raw = subprocess.check_output(['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5',
                               '-p', os.environ['AIFENCE_SSH_PORT'], os.environ['AIFENCE_SSH_TARGET'], remote])
credential = json.loads(raw)
service = b'AIfence isolated stand'
account = credential['email'].encode()
password = credential['password'].encode()
security = ctypes.CDLL('/System/Library/Frameworks/Security.framework/Security')
add = security.SecKeychainAddGenericPassword
add.argtypes = [ctypes.c_void_p, ctypes.c_uint32, ctypes.c_char_p, ctypes.c_uint32,
                ctypes.c_char_p, ctypes.c_uint32, ctypes.c_char_p, ctypes.c_void_p]
add.restype = ctypes.c_int32
status = add(None, len(service), service, len(account), account, len(password), password, None)
if status == -25299:
    print('Stand keychain item already exists; preserving it.')
elif status:
    print(f'Keychain add failed: OSStatus {status}', file=sys.stderr)
    sys.exit(1)
else:
    print('Stand login saved to the current user keychain.')

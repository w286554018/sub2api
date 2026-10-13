import os
import subprocess
import sys

sys.stdout.reconfigure(encoding='utf-8', errors='backslashreplace')
sys.stderr.reconfigure(encoding='utf-8', errors='backslashreplace')

timeout = int(os.environ.get('WSL_PIPE_TIMEOUT', '900'))
command = ['wsl.exe', '-d', 'Ubuntu-20.04', '--cd', '/tmp', '--exec', *sys.argv[1:]]
print('WSL_PIPE_COMMAND=' + repr(command), flush=True)
print('WSL_PIPE_TIMEOUT_SETTING=' + str(timeout), flush=True)
try:
    result = subprocess.run(command, stdin=subprocess.DEVNULL,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
except subprocess.TimeoutExpired as error:
    print((error.stdout or b'').decode('utf-8', errors='replace'), flush=True)
    print((error.stderr or b'').decode('utf-8', errors='replace'), flush=True)
    print('WSL_PIPE_TIMEOUT=' + str(timeout), flush=True)
    raise SystemExit(124)
print('WSL_STDOUT_BEGIN', flush=True)
print(result.stdout.decode('utf-8', errors='replace'), flush=True)
print('WSL_STDERR_BEGIN', flush=True)
print(result.stderr.decode('utf-8', errors='replace'), flush=True)
print('WSL_EXIT=' + str(result.returncode), flush=True)
raise SystemExit(result.returncode)

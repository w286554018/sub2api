import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

sys.stdout.reconfigure(encoding='utf-8', errors='backslashreplace')
sys.stderr.reconfigure(encoding='utf-8', errors='backslashreplace')

parser = argparse.ArgumentParser()
parser.add_argument('name')
parser.add_argument('--cwd', required=True)
parser.add_argument('--timeout', type=int, default=1800)
parser.add_argument('--env', action='append', default=[])
parser.add_argument('command', nargs=argparse.REMAINDER)
args = parser.parse_args()
command = args.command
if command and command[0] == '--':
    command = command[1:]
if not command:
    parser.error('command is required')
run_root = Path(os.environ['PI_SCRATCH_DIR']) / 'ranxi-integration'
run_root.mkdir(exist_ok=True)
state_path = run_root / (args.name + '.json')
log_path = run_root / (args.name + '.log')
if state_path.exists() or log_path.exists():
    parser.error('existing evidence must be preserved; use a new name')

def now():
    return datetime.datetime.now().astimezone().isoformat()

def git_value(*arguments):
    result = subprocess.run(['git', '-C', args.cwd, *arguments], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else None

def save():
    temporary = state_path.with_suffix('.json.tmp')
    temporary.write_text(json.dumps(state, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    os.replace(temporary, state_path)

environment = os.environ.copy()
changes = {}
for item in args.env:
    key, value = item.split('=', 1)
    if key.upper() in {'HOME', 'CODEX_HOME'}:
        parser.error('reserved environment key')
    environment[key] = value
    changes[key] = value
state = {'status': 'running', 'command': command, 'cwd': str(Path(args.cwd).resolve()),
         'input_head': git_value('rev-parse', 'HEAD'), 'input_index_tree': git_value('write-tree'),
         'environment': changes, 'started_at': now(), 'runner_pid': os.getpid(),
         'log_path': str(log_path), 'exit_code': None}
save()
started = time.monotonic()
try:
    with log_path.open('wb') as output:
        process = subprocess.Popen(command, cwd=args.cwd, env=environment, stdin=subprocess.DEVNULL,
                                   stdout=output, stderr=subprocess.STDOUT)
        state['command_pid'] = process.pid
        save()
        state['exit_code'] = process.wait(timeout=args.timeout)
    state['status'] = 'completed'
except subprocess.TimeoutExpired:
    if os.name == 'nt':
        subprocess.run(['taskkill', '/PID', str(process.pid), '/T', '/F'], capture_output=True)
    else:
        process.kill()
    process.wait()
    state['status'] = 'timed_out'
    state['error'] = 'recorded command timeout'
except Exception as error:
    state['status'] = 'runner_failed'
    state['error'] = repr(error)
finally:
    state['ended_at'] = now()
    state['elapsed_seconds'] = round(time.monotonic() - started, 3)
    if log_path.exists():
        data = log_path.read_bytes()
        state['log_bytes'] = len(data)
        state['log_sha256'] = hashlib.sha256(data).hexdigest()
    save()
print(json.dumps(state, ensure_ascii=False, indent=2))
if log_path.exists():
    tail = log_path.read_text(encoding='utf-8', errors='replace').splitlines()[-18:]
    print('\n'.join(tail))
sys.exit(state['exit_code'] if state['exit_code'] is not None else 124)

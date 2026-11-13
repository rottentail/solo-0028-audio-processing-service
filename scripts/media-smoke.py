#!/usr/bin/env python3
import json
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request

operation = sys.argv[1]
ffmpeg = os.environ.get('FFMPEG_PATH', 'ffmpeg')
with tempfile.TemporaryDirectory() as temp:
    binary = os.path.join(temp, 'server')
    source = os.path.join(temp, 'source.mp4')
    subprocess.run(['go', 'build', '-o', binary, './cmd/audioserve'], check=True)
    subprocess.run([ffmpeg, '-v', 'error', '-f', 'lavfi', '-i', 'testsrc=size=64x64:rate=10', '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=44100', '-t', '1', '-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-c:a', 'aac', source], check=True)
    with open(source, 'rb') as f:
        media = f.read()
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = 'http://127.0.0.1:%d' % port
    env = dict(os.environ, AUDIO_LISTEN='127.0.0.1:%d' % port, AUDIO_DATA_DIR=os.path.join(temp, 'data'))
    def request(path, data=None, method=None):
        with urllib.request.urlopen(urllib.request.Request(base + path, data=data, method=method), timeout=5) as response:
            return response.read()
    def start():
        process = subprocess.Popen([binary], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for _ in range(100):
            if process.poll() is not None:
                raise RuntimeError('service exited')
            try:
                request('/healthz')
                return process
            except urllib.error.URLError:
                time.sleep(.05)
        raise RuntimeError('service did not start')
    def stop(process):
        process.terminate()
        process.wait(timeout=10)
    def wait_job(job_id):
        for _ in range(200):
            result = json.loads(request('/v1/jobs/' + job_id))
            if result['state'] in ('completed', 'failed', 'cancelled'):
                return result
            time.sleep(.05)
        raise RuntimeError('job did not finish')
    process = start()
    try:
        job = json.loads(request('/v1/jobs?operation=' + operation, media))
        result = wait_job(job['id'])
        assert result['state'] == 'completed', result
        output = request('/v1/jobs/' + job['id'] + '/output')
        output_path = os.path.join(temp, 'result')
        with open(output_path, 'wb') as f:
            f.write(output)
        subprocess.run([ffmpeg, '-v', 'error', '-i', output_path, '-f', 'null', '-'], check=True)
        bad = json.loads(request('/v1/jobs?operation=' + operation, b'not media'))
        assert wait_job(bad['id'])['state'] == 'failed'
        retried = json.loads(request('/v1/jobs/' + bad['id'] + '/retry', b''))
        assert retried['attempt'] == 2 and wait_job(bad['id'])['state'] == 'failed'
        stop(process)
        process = start()
        assert json.loads(request('/v1/jobs/' + job['id']))['state'] == 'completed'
        assert request('/v1/jobs/' + job['id'] + '/output') == output
        print(operation + ': passed')
    finally:
        if process.poll() is None:
            stop(process)

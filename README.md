# Audio Processing Service

A Go HTTP service for synchronous PCM WAV operations and asynchronous audio/video conversion. WAV requests support little-endian integer PCM, 16-bit samples, and one or two channels, with a 16 MiB limit. Media jobs accept up to 64 MiB. Unsupported encodings, malformed chunks and invalid parameters return HTTP 400; oversized WAV bodies return 413.

## Run

Requires Go 1.22 or newer and FFmpeg with libx264 and AAC support on PATH. Python 3 is needed only for the smoke checks. Set `FFMPEG_PATH` to select a different executable.

```sh
go build ./...
go run ./cmd/audioserve
```

`AUDIO_LISTEN` controls the listening address; the default is `127.0.0.1:8080`. The service has no authentication and should be deployed behind an appropriate trusted gateway when exposed to other hosts.

`AUDIO_DATA_DIR` selects persistent job storage (default `./audio-data`). Use a dedicated directory for one service process only; no cross-process locking, quota, automatic expiration, or deletion is provided. Retained inputs can contain sensitive data. Configure external disk quotas and cleanup according to the deployment's needs. Two workers process at most 32 waiting jobs; processing is limited to five minutes per attempt.

## HTTP API

Send the WAV bytes as the POST body. Responses are JSON for inspection and `audio/wav` for transformations.

```sh
curl --data-binary @input.wav http://127.0.0.1:8080/v1/inspect
curl --data-binary @input.wav 'http://127.0.0.1:8080/v1/clip?start=0&end=8000' -o clip.wav
curl --data-binary @input.wav 'http://127.0.0.1:8080/v1/gain?factor=1.5' -o louder.wav
```

Clip bounds are zero-based frame indices, with an inclusive start and exclusive end. Gain ranges from 0 to 4 and saturates at 16-bit limits. Output is newly encoded PCM; ancillary WAV chunks are not preserved. `GET /healthz` checks availability.

Submit media bytes for asynchronous processing:

```sh
curl --data-binary @input.mp4 'http://127.0.0.1:8080/v1/jobs?operation=video-mp4'
curl http://127.0.0.1:8080/v1/jobs/JOB_ID
curl http://127.0.0.1:8080/v1/jobs/JOB_ID/output -o result.mp4
curl -X DELETE http://127.0.0.1:8080/v1/jobs/JOB_ID
curl -X POST http://127.0.0.1:8080/v1/jobs/JOB_ID/retry
```

Operations are `video-mp4`, `audio-m4a`, and `thumbnail`. Query the job before retrieving output: only `completed` jobs expose it. A full queue returns 503. Cancellation is persistent; retry can return 409 until the old process exits. On restart, unfinished jobs become `failed` and can be retried explicitly. The API stores media and metadata on disk; it does not execute arbitrary user-provided FFmpeg arguments.

Video jobs can specify `start` and `duration` together in seconds, and `width` and `height` together in pixels. Start must be 0 to 86400, duration greater than 0 and at most 3600, and dimensions even and between 16x16 and 1920x1080. Video is scaled to fit and padded without changing its aspect ratio. The same time interval is applied to audio. For example:

```sh
curl --data-binary @input.mp4 'http://127.0.0.1:8080/v1/jobs?operation=video-mp4&start=1.5&duration=2&width=640&height=360'
```

Parameters are stored with the job and reused for every attempt. Output decoding is checked before the job becomes completed. No duration probing or exact frame-boundary guarantee is offered; encoding timestamps and source keyframes affect the available frames.

An abrupt service termination cannot guarantee that its FFmpeg child has exited. Each attempt uses a distinct temporary output name so a retry cannot consume that child's output; an orphan process or temporary file may still require operating-system supervision or cleanup. The service does not provide power-loss durability guarantees.

## Structure and checks

`cmd/audioserve` contains the process entry point, `internal/httpapi` the HTTP boundary, `internal/wav` RIFF parsing and encoding, `internal/audio` frame transforms, `internal/profile` processing parameters, and `internal/jobs` persistent job execution. Run `python3 scripts/smoke.py inspect`, `python3 scripts/smoke.py clip`, and `python3 scripts/smoke.py gain`, plus `python3 scripts/media-smoke.py` with `video-mp4`, `audio-m4a`, `thumbnail`, or `video-profile`, and `python3 scripts/control-smoke.py` for independent offline HTTP checks.

Automated test files are intentionally deferred. A later engineering task should add unit, fuzz, concurrency, crash-recovery and HTTP integration tests; the smoke checks exercise production entry points in the meantime. Arbitrary filters, streaming input, multi-host workers and automatic storage cleanup are outside this baseline.

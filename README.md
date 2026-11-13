# Audio Processing Service

A Go HTTP service for inspecting and transforming uncompressed WAV audio without retaining request bodies. It supports little-endian integer PCM, 16-bit samples, and one or two channels. Inputs are limited to 16 MiB. Unsupported encodings, malformed chunks and invalid parameters return HTTP 400; oversized bodies return 413.

## Run

Requires Go 1.22 or newer. Python 3 is needed only for the smoke checks.

```sh
go build ./...
go run ./cmd/audioserve
```

`AUDIO_LISTEN` controls the listening address; the default is `127.0.0.1:8080`. The service has no authentication and should be deployed behind an appropriate trusted gateway when exposed to other hosts.

## HTTP API

Send the WAV bytes as the POST body. Responses are JSON for inspection and `audio/wav` for transformations.

```sh
curl --data-binary @input.wav http://127.0.0.1:8080/v1/inspect
curl --data-binary @input.wav 'http://127.0.0.1:8080/v1/clip?start=0&end=8000' -o clip.wav
curl --data-binary @input.wav 'http://127.0.0.1:8080/v1/gain?factor=1.5' -o louder.wav
```

Clip bounds are zero-based frame indices, with an inclusive start and exclusive end. Gain ranges from 0 to 4 and saturates at 16-bit limits. Output is newly encoded PCM; ancillary WAV chunks are not preserved. `GET /healthz` checks availability.

## Structure and checks

`cmd/audioserve` contains the process entry point, `internal/httpapi` the HTTP boundary, `internal/wav` RIFF parsing and encoding, and `internal/audio` frame transforms. Run `python3 scripts/smoke.py inspect`, `python3 scripts/smoke.py clip`, and `python3 scripts/smoke.py gain` for independent offline HTTP workflow checks.

Automated test files are intentionally deferred. A later engineering task should add unit, fuzz, and HTTP integration tests; the smoke checks exercise production entry points in the meantime. Compressed media, video, streaming uploads, and persistent storage are outside this baseline.

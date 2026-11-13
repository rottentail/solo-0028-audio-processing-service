# Audio Processing Service

An HTTP service for media engineers to inspect and transform uncompressed PCM WAV audio. It accepts bounded request bodies and returns computed metadata or a new WAV stream; it does not retain uploaded media.

## Entities and rules

An audio stream has a sample rate, channel count, bit depth, frame count and interleaved PCM samples. Only integer PCM, mono or stereo, 16-bit samples are accepted. RIFF chunk lengths and frame alignment must be valid. A clip uses a half-open frame interval within the stream. Gain is a finite multiplier from 0 through 4 with saturated samples.

## Workflows

1. Inspect: decode an uploaded WAV and return metadata as JSON.
2. Clip: validate a frame interval, slice full frames and encode the result as WAV.
3. Gain: validate a multiplier, scale and saturate samples, and encode a WAV result.

## Modules and interfaces

`cmd/audioserve` starts the server; `internal/httpapi` validates HTTP inputs and maps errors; `internal/wav` decodes and encodes RIFF PCM; `internal/audio` implements frame operations. POST `/v1/inspect`, `/v1/clip?start=0&end=1`, and `/v1/gain?factor=1.5` accept WAV bodies. A GET `/healthz` indicates process availability.

## Validation plan

Build the executable and exercise each endpoint against generated PCM audio using bounded offline smoke checks, including invalid input and transformed output checks. Automated test files are intentionally deferred to a later engineering task, which should add unit, fuzz, and HTTP integration coverage.

# RC17 perspective matting experiments, 2026-10-26

Base: `ez-2.4.2-rc.17-resize8`, `5a27bd847af0486c28d6936e701b3ae962bf383a`.
Engineering workspace: `<historical-workspace>/rc17-perspective10-20261026`.
Status: user accepted the controlled multi-background prototype as usable and asked to stop further picture-quality refinement. This is not a completed universal automatic-calibration release.

## Confirmed observations

| Experiment | Result | Limit |
| --- | --- | --- |
| XHS locked 60M downlink, previous client | Grids remained; user reported less flashing | PC-to-camera encoder was still 8M; does not rule out the entire compression chain |
| Spatial transmittance model, controlled Camera2 fixture | User reported much cleaner than XHS, or no visible particles | Known static gradients and scrim; not an arbitrary application |
| Continuous blue input, WeWork | User reported fewer grids, remaining sparse stripes | No desktop recognition input during this condition; restored normal 10fps afterward |
| Native opaque blue control on blue backing | Original one-color path removed it and exposed the white desktop | One image cannot generally distinguish matching foreground and backing colors |
| Black/white reference, held-out gray, static fixture | Mean absolute RGB error 0.3363/255; 95th percentile 1.5059/255 | Scan animation was deliberately disabled only in the disposable test app |
| Known 40% black scrim | Estimated alpha median 0.40392 | Decoder/codec quantization included |
| Normal/interleaved vs continuous-blue fixture matte | Quiet clear and scrim ROIs both had alpha standard deviation 0 | Does not prove dynamic UI is noise-free |
| Three black/white/gray rounds, live WeWork animation | Gray prediction MAE 4.0865 -> 3.7415/255 using a three-round median; P95 18.3294 -> 16.5529/255 | Motion or scene-dependent UI breaks the static foreground assumption; cause not uniquely isolated |
| Independent static reference, first prototype | Blue core restored; user saw white outline and horizontal bands | Strict confidence rejection and hard reference/live transition were insufficient at edges |
| Revised static reference and soft reference/live transition | User: blue border and bands both improved, small remnants remain, sufficiently usable; stop refining | Confirmed on the controlled fixture, not universal real-app integration |

The controlled test has only CAMERA permission, no network/storage permission, no account data, and refuses to open a camera outside a virtual device. It was uninstalled after the session.

## Implemented code

- `perspective_color.h`: metadata-aware BT.601/BT.709/BT.2020 and full/limited-range handling. Calibration classification and local RGB conversion use matching coefficients. Unspecified matrices follow the ordinary SDL path's BT.709 fallback.
- Actual controlled and WeWork streams reported matrix 5 / limited range, so a matrix mismatch was not established as their primary cause. The change fixes inconsistent assumptions for other metadata combinations.
- Existing 32x64 spatial key-plane stabilization retains native dark scrims and smooths flat-field codec noise.
- Optional full-resolution static reference: premultiplied foreground RGB plus alpha, uploaded once to a GPU texture. A sentinel represents invalid reference pixels. The observed current pixel is compared with the predicted blue-backed reference; a smooth weight selects static reconstruction versus live animation processing.
- The corrected controlled reference uses the measured blue-backed response at colored UI edges, with black/white alpha and gray validation. Background areas retain the stable black foreground model. This reference belongs to that exact fixture/layout.
- Startup and shutdown phase logging was added to `PerspectiveCamera`; the startup deadline now reports its last phase. No system camera service was restarted or security policy changed by the agent.

The approved calibration art and fade remain unchanged. The mDNS/resize8 work is retained.

## Explicit engineering controls

These are unset in ordinary launches and are not user-facing options:

- `SCEZ_PERSPECTIVE_TEST_CONTROL`: a local text file with numeric input mode and optional bitrate. Modes: 0 normal, 1 blue only, 2 desktop only, 3 black, 4 gray, 5 white, 6 dark red. No command execution or arbitrary payload parsing.
- `SCEZ_PERSPECTIVE_TEST_BITRATE`: bounded 1M..60M initialization override; ordinary camera input remains 8M.
- In a diagnostic NVENC session only, a valid second number requests bitrate reconfiguration. The 8M/32M comparison was prepared but **not executed** before the user stopped quality refinement. A request log alone is not evidence of successful driver reconfiguration.
- `SCEZ_COLOR_PROBE_DIR`: bounded, overwritten per-mode raw/matte samples. Removed during cleanup.
- `SCEZ_LOCAL_REFERENCE`: opt-in PNG reference for the controlled experiment. No such fixture reference is automatically applied to real applications. No automatic multi-background acquisition/state machine has been shipped.

An approximately equal-BT.709-luma dark-red/blue condition was prepared to examine whether black/white changes cause application brightness adaptation. **It was not run to completion**, and equal decoded luminance was not verified.

## Reliability findings and unfinished items

- XHS initially showed a page and then went black. Its process had native crash records, including `SIGABRT` in a probe/service thread. Do not attribute this uniquely to lock state or anti-tamper behavior. Further XHS restarts were stopped.
- Physical lock status alone is not sufficient to determine independent-display usability. Logs showed the virtual display awake and a visible task even while the physical device was locked. An application may still fail independently.
- Recreating a tablet virtual camera timed out twice. Old logs do not identify the exact blocked Binder stage. Clearing background apps and unlocking was followed by a successful attempt, but causality was not established. The user did not report a reboot.
- Early test scripts had 900/1800-second automatic exit limits; an active-camera timeout is a teardown confounder. Those automatic limits were removed from the engineering launchers. Final cleanup first sent Back to the controlled activity and observed `PERSPECTIVE_ACTIVE=0` before ending the client.
- Capture exclusion during active perspective still prevents ordinary screenshot/recording of that window on the current Windows build. No unvalidated visibility workaround was added.
- Automatic multicolor calibration for arbitrary dynamic app UIs remains future work. Longer calibration alone cannot establish it.

## Verification and artifact status

- Native calibration state/fade, camera-region/cropped locator mapping, color matrix/range, and GPU bounds/reference tests pass.
- The GPU test checks blue native UI preservation and immediate fallback when a green animation replaces that pixel.
- Native protocol remains `5.0.1-ez2.4.2-rc.17-resize8` for compatibility.
- Latest native SHA256: `5A57D4BAD8B28BBE7DDF9AC03FF1F9F7D043D62021337E1ADD8DBBA3D82A475B`.
- Latest server SHA256: `1AE24EB1CE3519C75F42FFA2FE1D4C4F6A683ECD014231AA1E98E44E2CDA5A4E`.
- Trial runtime is staged under `<runtime-root>/scrcpy-ez-2.4.2-rc.17-perspective10/scrcpy-ez-2.4.2-rc.17-perspective10`; it is not a finalized public package. Its inherited README/source manifest still need release regeneration. No commit, push or release was made in this experiment phase.
- Temporary sampled frames and obsolete experiment executables are removed. Scalar metrics, source/tests, and the single controlled-reference test asset are retained.

## Primary references

- [Smith and Blinn, Blue Screen Matting](https://www.microsoft.com/en-us/research/publication/blue-screen-matting-2/): multiple known backgrounds resolve ambiguity under a constant-foreground assumption.
- [FFmpeg libswscale](https://www.ffmpeg.org/doxygen/8.1/group__libsws.html): explicit matrix and range configuration.
- [FFmpeg NVENC implementation](https://github.com/FFmpeg/FFmpeg/blob/master/libavcodec/nvenc.c): bitrate reconfiguration depends on encoder/driver support.

> 历史研究记录的脱敏副本。保留当时的实验条件、数值与阶段判断；后续结论以[总记录](../../../../透视相机研发全记录_rc17至rc25_20261103.md)为准。历史命令不作为当前通用启动入口，原始日志、APK和图像未随本文件公开。

# inverse6 offline simulation report

Date: 2026-10-28. Owner: sim-analyst (shared task task-2). Scope: `inverse6-sim/` only.
Status: **offline numeric validation only** (no device). Sim is a 15-frame synthetic
sweep derived from the single real native capture `light-diagnostic/native-blue-best.png`;
it is a controlled stress test, not a live-path measurement.

## 1. Exact commands

```powershell
$py='<user-home>\.dsh\dsh-runtimes\dsh-primary-runtime\dependencies\python\python.exe'
# native PNG characterization (existing probe)
& $py <historical-workspace>/rc19-native-alpha-flex-20261028\inverse6-sim\probe_native_light.py
# full simulation: metrics.json + 3 comparison PNGs, runtime ~112 s
& $py <historical-workspace>/rc19-native-alpha-flex-20261028\inverse6-sim\sim.py
```

Python 3.13 + numpy + Pillow (runtime-provisioned; nothing installed). No git commands.
Outputs in `<historical-workspace>/rc19-native-alpha-flex-20261028/inverse6-sim/`:
`metrics.json` (full numbers), `comparison_glow.png`, `comparison_halo_zoom.png`,
`comparison_sweep.png`, `sim_run.log`.

## 2. Simulation setup (what is being compared)

* Native `native-blue-best.png`: 576x1280 straight RGBA, premultiplied in sim.
  Scrim alpha mode = **76/255 = 0.2980**, scrim rgb exactly 0. Light rows 506..655
  (full width). Light layer: `a_light = clip((alpha-scrim)/(1-scrim),0,1)`,
  premult rgb above scrim = premult rgb of the capture.
* Clean native baseline `n` = capture with the moving light layer replaced by the scrim
  (the ideal native min-alpha learner inside the motion envelope); static UI outside is
  identical to the capture. Motion envelope rows **376..785** (410 rows, all scrim
  in `n`, no static UI). Envelope gating: outside it output = `n` exactly.
* 15 sweep positions `dy = -120..+120` (step ~17 px); `V = L_shifted over n`
  (premultiplied), 8-bit quantized; clean target = noise-free V at the same position.
* Motion-envelope gating applied to inverse6; inverse5 evaluated with its gate
  (noise contribution in background is ~0 there, see table).
* Metric space: premultiplied RGBA 0..1, all errors in 1/255 ULP (so 1 ULP = 1 output
  byte step). "MAE" = mean |error| per RGBA component. Bands from clean light alpha
  `a_light`: fringe 0.5%-2%, **halo 2%-25%**, body >25%; background = `a_light==0`.
  "bg far" = uncapped background >8 rows away from the lit band (true noise floor);
  "bg" also includes the 1-2 px support rim next to the bright body.
* Keying/codec noise synthesized (see §5).

## 3. Key result: inverse5 vs inverse6 vs original

Noise-limited values: the *original candidate* (`out=V`) is the reference for "how good
can it get at this noise"; "intrinsic" = gate behaviour on the noise-free V.

| candidate | bg far max | bg max | bg bytes differ | halo MAE | halo alpha gain | fringe MAE | fringe alpha gain | body MAE |
|---|---|---|---|---|---|---|---|---|
| inverse5 (blue gate) | **0.0** | 10.0 | 0.2% | **10.53** | **0.246** | 1.58 | **0.000** | 2.57 |
| inverse6 design LO=.0035 HI=.015 r=2 | 11.0 | 11.0 | 37.8% | **1.42** | **1.044** | 1.02 | 1.287 | 1.48 |
| inverse6 scan-best LO=.007 HI=.020 r=4 | 5.0 | 10.0 | 1.8% | 1.42 | 1.044 | 0.95 | 1.065 | 1.48 |
| original (out=V) | 13.0 | 13.0 | 96.7% | 1.48 | 0.977 | 1.20 | 0.991 | 1.57 |

Intrinsic (noise-free) gate alpha gain:

| gate | halo 2-25% | fringe 0.5-2% |
|---|---|---|
| inverse5 | 0.251 (loses 75%) | 0.000 (loses 100%) |
| inverse6 design .0035/.015 r2 | **1.000** | **0.881 (loses 12%)** |
| inverse6 scan-best .007/.020 r4 | 0.999 | 0.586 (loses 41%) |

Conclusions:

* **inverse5 destroys the soft halo**: it keeps only 24.6% of the halo-band alpha and 0%
  of the outer fringe; halo MAE 10.53 ULP (~7x the noise floor). This is exactly the
  "dynamic scan component not clean" complaint (hard cutoff at the b-excess≈0.15 isogram).
* **inverse6 restores the full halo**: halo alpha gain 1.000 intrinsic (1.044 measured —
  the +4.4% is the 0.74 ULP model bias, see §6), fringe 0.881, and its halo MAE 1.42 ULP
  is at the noise floor (original V has 1.48 ULP). Background noise is essentially not
  transferred: gated output outside the envelope is byte-identical to native, and the
  un-gated noise floor in the static patch is 0.22 ULP RGB / 0.70 ULP alpha temporal std
  vs 0.83/1.92 for the original candidate.

## 4. Answers to the specific retune questions

### 4a. Where does the design variant's bg_far_max=11.0 ULP come from?

Verified pixel: frame 12, **row 538, col 312**, `dmax=11.0 ULP`, `support=3.6 ULP`,
`active=0.989` ⇒ output deviates from native by 11 ULP (4.3/255) at that single pixel.
Local 5x5 dmax (ULP):

```
0 1 1 3 2
1 3 4 4 4
2 3 11 1 4
3 1 2 2 2
4 3 2 1 4
```

* **(i) min-alpha model bias** — real but small: model alpha is
  **-0.74 ULP** below native scrim (15-frame warm-up; theoretical -0.95 ULP; with the
  real ~600-frame warm-up theory predicts ~-1.7 ULP). This is a *systematic* +0.74 ULP
  floor on alpha diff and is why 37.8% of background bytes differ (most by exactly 1 ULP;
  mean |dev| 0.16 ULP). It is **not** the 11 ULP spike.
* **(ii) 5-tap pre-average smearing at the light boundary** — **not the cause here**: the
  worst pixel is in the far background (>80 rows above the light band for that frame).
* **(iii) threshold letting noise through** — **yes, this is the dominant mechanism**: the
  spike is a single-pixel gaussian tail of the rectified `max` over 4 channels
  (neighbours are 1-4 ULP, so it is *not* 8x8-block-correlated and *not* model residual).
  It reaches `dmax=11`; the 5x5 support center weight is only 0.1406, so
  `support = 0.14*11 + neighbours ≈ 3.6 ULP`. Smoothstep(LO=.0035, HI=.015) at
  support=3.6 ULP (=0.0141) is ≈0.99, so the spike passes at full amplitude.
  The rectification/`max`-over-4-channels and the +0.74 ULP bias raise the typical
  background support to ~2 ULP (design §4 assumed 0.4-0.8 ULP after 5x5); the max
  percentile is p99.9 ≤ 5.0 ULP per frame, max 11 ULP over the whole sequence.

Retune effect (ULP, from the 40-config scan): raising **LO alone does not help**
(LO=.009/HI=.015 still bg_far_max=11 — the spike's support is well above LO); raising
**HI helps**: HI=.020 → 8-9, HI=.030 → 3-5. So a LO/HI retune does "help the metric",
but at the cost of the halo fringe (§4b), i.e. it trades the user-visible property for a
rare 1-pixel artifact. The right fix for spikes is a 3x3 min prefilter on `d` or a
~1-2 ULP dead-zone before the 5x5 support (measured in §5), not new LO/HI.

### 4b. Quantitative halo restoration / attenuation

See §3. Answer to "how much does inverse6 attenuate":

* design **LO=.0035/HI=.015/r=2**: halo band 2-25% → intrinsic gain **1.000** (no
  attenuation), fringe 0.5-2% (outermost faint edge) → **0.881** (loses 12%).
* scan-best **LO=.007/HI=.020/r=4**: halo band → 0.999 (same), fringe → **0.586**
  (loses 41%). It is better on background (bg_far 5 vs 11 ULP, bytes-differ 1.8% vs
  37.8%) because the higher HI suppresses the tiny residual deviations too, but it is
  **worse for keeping the full soft halo** — the user-priority property. Not recommended.
* inverse5: halo 0.251, fringe 0.000.
* Measured halo MAE vs clean target: inverse5 **10.53 ULP** vs inverse6 design
  **1.42 ULP** (7.4x better; original V 1.48). Alpha MAE in halo: 13.43 vs 1.75 ULP.
* Measured halo alpha gain inverse6 design 1.044 (vs intrinsic 1.000) — a +0.74 ULP
  additive lift from the model min-alpha bias; for the halo band (mean light increment
  17.8 ULP) that is +4%; it is larger in relative terms only for the fringe.

### 4c. Synthesized noise magnitudes, and is it harsher than the real keyed path?

Noise added to every `V` frame (learn and live), all in output ULP (1 ULP = 1/255):

| component | magnitude |
|---|---|
| per-pixel gaussian, rgb | sigma = **1.5 ULP**, independent per pixel/channel/frame |
| per-pixel gaussian, alpha | sigma = **2.0 ULP** |
| 8x8 block DC bias | **+-1.5 ULP**, fixed pattern for the whole sequence (cancels in the model) |
| isolated speckles | **40 per frame**, per-channel positive **10-30 ULP** on rgb and alpha (single pixel) |
| static key-plate alpha bias band | alpha **-3 ULP** on rows 680..1000 (learned by the model, cancels in background) |

Honest assessment: **this is conservative/harsher than the real 576x1280@60fps,
~15 Mbps H.264 keyed-over-flat-green path is likely to be.** 15 Mbps over
576*1280*60 = 44.2 Mpx/s is ~0.34 bit/px — high quality; with a static UI over a flat
green virtual-camera plate, the encoder mostly skips/copies static blocks, so decoded
temporal noise in the background is likely ~0.5-1 ULP (and largely *temporally stable*,
hence even better cancelled by the min-alpha model). Isolated 10-30 ULP single-pixel
keyer artifacts on 40 pixels/frame are a worst-case stress test, not typical; the
"-3 ULP over 25% of the frame" key-plate bias is also a pessimistic stand-in for real
keyer/model mismatch.

Under a more realistic ~1 ULP-class noise (sigma_rgb=0.75, sigma_a=1.0 ULP, no large
speckles): the min-alpha bias scales with sigma → ~0.37 ULP; the background support
floor becomes ~0.5-1 ULP, i.e. at/below LO=0.0035 (0.89 ULP), so most far-background
pixels get active=0 and the background would be byte-identical (modulo +-1 ULP rounding);
worst rectified spikes ~5 ULP produce support ~1.5-2 ULP < HI (3.8 ULP) → active ≲0.2,
deviation ≤1-2 ULP. **So at realistic noise the design constants are appropriate and the
background should be effectively clean.**

### 4d. Final recommendation

**Keep `LO=0.0035`, `HI=0.015`, r=2** (design constants). Rationale: it is the only
variant that preserves the soft halo (fringe gain 0.88 vs 0.59 for the bg-optimal config),
and at realistic (1 ULP-class) keying noise the background is clean. Do **not** raise HI
to fix the 11 ULP background outlier: it costs 59-88% of the faint outer fringe.

One-line risk: with the conservative stress noise, rare single-pixel spikes pass at
nearly full amplitude (up to ~11 ULP = 4% alpha, p99.9 ≤5 ULP) in unlit background and the
background is not byte-identical (37.8% of pixels differ from native, almost all by 1 ULP);
if the live path is really that noisy, add a **3x3 min prefilter on `d` before the 5x5
support** (or subtract a 1-2 ULP dead-zone) — measured: bg max 11→1 ULP, bytes-differ
37.8%→0.28%, halo band unchanged, but the faintest 0.5-2% fringe loses ~half. Prefer that
over changing LO/HI.

## 5. Implementation variants (all at scan-best LO=.007/HI=.020/r=4 unless noted)

| variant | bg far max ULP | bg bytes differ | halo alpha gain | fringe alpha gain | halo MAE |
|---|---|---|---|---|---|
| standard | 5.0 | 1.8% | 1.044 | 1.065 | 1.42 |
| + dead-zone 1.0 ULP | 2.0 | 0.79% | 1.043 | 0.576 | 1.42 |
| + dead-zone 1.5 ULP | 1.0 | 0.76% | 1.042 | 0.335 | 1.42 |
| + dead-zone 2.0 ULP | 0.0 | 0.75% | 1.040 | 0.138 | 1.43 |
| + model = mean of 3 lowest-alpha samples | 4.0 | 1.13% | 1.029 | 0.925 | 1.39 |
| + 3x3 min prefilter on d | 0.0 | 0.44% | 1.038 | 0.070 | 1.44 |
| **design + 3x3 min prefilter** | **1.0** | **0.28%** | **1.042** | **0.483** | **1.42** |
| + per-frame 8x8 flicker DC +-0.75*1.5 ULP | 30.0 | 5.8% | 1.044 | 1.096 | 1.51 |

Notes/caveats on this table:
* Rows 1-6 use the scan-best config, so their fringe gain is already handicapped by
  HI=.020; the "design+min3x3" row is the directly usable combination.
* The "dead-zone"/"min3x3" fixes reduce the *far* background to 0-1 ULP but the `bg max`
  stays ~10 ULP: that residual maximum is the **support rim** (unlit pixels within the
  support radius of the bright body where neighbour support legitimately forces
  active≈1, so local keying noise passes). It is hidden by the glow itself; the far
  noise floor is what matters for visible "dirty background".
* **Flicker variant is a warning, not a measurement**: if the real codec's per-8x8 DC
  varies frame to frame instead of being temporally stable, the min-alpha model cannot
  cancel it and background deviation jumps to ~30 ULP. Check on-device temporal
  stability of the keyed background; if it flickers, the model (not LO/HI) needs work.
* The *speckle_leak* field in `metrics.json` has a known sim limitation: speckles are
  drawn uniformly over the frame, and most sampled speckles that matter land within the
  support radius of the moving bright body where active≈1 is legitimate, so the reported
  "mean leak 25 ULP" is dominated by those, not by isolated background speckles. Treat
  the `bg far` metric (design+prefilter = 1.0 ULP) as the isolated-noise result; do not
  quote mean speckle leak as an isolated-speckle test.
* The flicker variant's noise draw differs from the main live frames, so its
  speckle-related fields are not comparable to the other rows.

## 6. Learned model: min-alpha bias and its effect

Measured on 482k never-lit scrim pixels (outside the -3 ULP key band), model learned
from 15 frames with 5-tap spatial pre-average:

* model alpha vs native scrim: **mean -0.74 ULP** (median -1.0, p1 -3.0, min -4.0)
  → the model is slightly *too transparent*, i.e. every active output gets
  **+0.74 ULP alpha** added (and the background `d` gets the same positive floor;
  the rectified max-over-4-channels adds ~+1-2 ULP more).
* model rgb |mean| = 0.84 ULP — this is mostly the *stored* static 8x8 block bias
  (it is re-added by every live V and therefore largely cancels in `V-model`); the
  residual (~0.5 ULP at 8x8 block edges where the 5-tap blur mixes neighbouring block
  biases) plus the +0.74 ULP alpha floor are what make 37.8% of background bytes differ,
  nearly all by exactly 1 ULP.
* theory: sigma of 5x5-averaged alpha noise = 0.55 ULP; E[min] for N=15 = -1.74 sigma
  = -0.95 ULP (measured -0.74, consistent). For the real 10 s @60 fps warm-up
  (N~600) theory predicts **~-1.7 ULP**; if real noise is lower this stays small, but
  the bias is monotone in both sigma and N — worth an on-device check.
* effect on the light: additive +0.74 ULP alpha (relative +4.2% on the halo band,
  +28% on the 0-2% fringe, negligible +0.9% on the bright body) plus a multiplicative
  light-alpha change from the -3 ULP key band (+1.7% of the light increment). Both are
  invisible in practice; the halo gain table already includes the additive part.

## 7. Visual deliverables

`comparison_glow.png`: 3x2+3 grid, frame dy=0, crop rows 455-725, 2x nearest, composited
over 0.35 gray: native n, inverse5, inverse6 (scan-best config), clean target, original V,
|error| heat maps (x18) and "missed halo alpha" maps (x8).
`comparison_halo_zoom.png`: rows 470-580, 4x nearest: native / inverse5 / inverse6 /
clean target / missed-halo maps.
`comparison_sweep.png`: sweep positions dy=-120, 0, +120; inverse5 / inverse6 / clean
target, 2x nearest.

What they show: inverse5 produces only the bright core and cuts the entire soft halo
(the "missed halo alpha" map is saturated); inverse6 reproduces the clean target's full
soft halo up to the noise floor (its error map is flat noise-level). The main comparison
uses the scan-best config (LO=.007/HI=.020/r4); with the recommended design constants
(LO=.0035/HI=.015/r2) its faint outer fringe is closer to the clean target still.

## 8. Risks and limits of this validation

1. Synthetic sweep from one static capture: no real keyer color-difference traces, no
   camera-scene dynamics. "Original candidate" background here is only noisy, so its real
   historic color-trace defect is not reproduced; comparisons of *background quality*
   between candidates are optimistic for all of them.
2. Motion envelope is assumed ideal (row band, frozen, correct); envelope errors are not
   tested. In production, envelope edges could clip/step the light.
3. Background byte-identity holds only outside the envelope. Inside the envelope it is
   *not* byte-identical under the conservative noise: 37.8% of unlit pixels differ from
   native (mean 0.16 ULP, almost all exactly 1 ULP; max 11 ULP single-pixel spike at the
   p99.9<=5 ULP / max 11 ULP level). With the 3x3 prefilter fix: 0.28% differ, max 1 ULP
   far from the light (rim remains).
4. Static 8x8 DC is assumed; per-frame codec flicker degrades the model badly (30 ULP
   background in the variant) — needs an on-device temporal-stability check.
5. Model is learned and evaluated on the same 15 sweep positions; real warm-up/live are
   different frames (fine) but the same static scene (model staleness/refresh not tested;
   design already lists this).
6. Noise model is deliberately conservative (see 4c); absolute bg/deviation numbers should
   be read as an upper bound, relative comparison inverse5-vs-inverse6 is robust.
7. Only additive/alpha-increasing dynamics are modeled (per design limitation); darkening
   content is out of scope.

## 9. Files

* `sim.py` — full simulation (native analysis → V synthesis → model learning → inverse5/
  inverse6 → 40-config scan → variants → metrics → PNGs).
* `explore_native.py` — native PNG characterization.
* `metrics.json` — full numbers (model stats, per-band metrics, 40-config scan,
  intrinsic scan, variants, noise transfer).
* `comparison_glow.png`, `comparison_halo_zoom.png`, `comparison_sweep.png`.
* `sim_run.log` — run log of the final run (112 s).

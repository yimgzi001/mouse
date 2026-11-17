"""Analyse the difference between Go landmarks and the MediaPipe reference.

For each hand it fits a similarity transform (scale, rotation, shift) from the Go
points to the reference points. A consistent scale or shift across images would
point at a geometry bug; random residuals point at model sensitivity.
Usage: python tools/compare_landmarks.py <go_dump.json>
"""
import json
import os
import sys

import numpy as np

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ref = json.load(open(os.path.join(root, "testdata", "reference", "mediapipe_landmarks.json"), encoding="utf-8"))
go = json.load(open(sys.argv[1], encoding="utf-8"))

NAMES = ["wrist", "th1", "th2", "th3", "thTip", "ix1", "ix2", "ix3", "ixTip", "md1", "md2", "md3", "mdTip",
         "rg1", "rg2", "rg3", "rgTip", "pk1", "pk2", "pk3", "pkTip"]
per_landmark = np.zeros(21)
n = 0
for name in sorted(go):
    for h in go[name] or []:
        g = np.array([[p["X"], p["Y"]] for p in h["Landmarks"]])
        best = None
        for rh in ref[name]["hands"]:
            r = np.array(rh["landmarks"])[:, :2]
            d = np.linalg.norm(g - r, axis=1)
            if best is None or d.mean() < best[0].mean():
                best = (d, r)
        d, r = best
        # similarity fit g -> r
        gc, rc = g.mean(0), r.mean(0)
        g0, r0 = g - gc, r - rc
        a = (g0 * r0).sum() / (g0 ** 2).sum()
        b = (g0[:, 0] * r0[:, 1] - g0[:, 1] * r0[:, 0]).sum() / (g0 ** 2).sum()
        scale = np.hypot(a, b)
        rot = np.degrees(np.arctan2(b, a))
        fitted = np.stack([a * g0[:, 0] - b * g0[:, 1], b * g0[:, 0] + a * g0[:, 1]], 1) + rc
        resid = np.linalg.norm(fitted - r, axis=1)
        roi = h["ROI"]
        # shift expressed in the hand's own axes (x = across the palm, y = towards the wrist), as % of the crop
        ang = roi["Rot"]
        sh = rc - gc
        sx = (sh[0] * np.cos(ang) + sh[1] * np.sin(ang)) / roi["W"] * 100
        sy = (-sh[0] * np.sin(ang) + sh[1] * np.cos(ang)) / roi["W"] * 100
        worst = int(d.argmax())
        print(f"{name:26s} mean={d.mean():5.2f} max={d.max():5.2f}@{NAMES[worst]:6s} scale={scale:.4f} rot={rot:+.2f}deg "
              f"shift=({sx:+.2f}%,{sy:+.2f}%) residual={resid.mean():.2f}px roi={roi['W']:.0f}")
        per_landmark += d / roi["W"] * 100
        n += 1
print("mean error per landmark, % of crop:")
print("  " + " ".join(f"{NAMES[i]}={per_landmark[i] / n:.2f}" for i in range(21)))

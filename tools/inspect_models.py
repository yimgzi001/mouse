"""Print input/output signatures of the ONNX models and locate the MediaPipe reference models."""
import glob
import os
import sys

import onnxruntime as ort

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
for name in ("palm_detection.onnx", "hand_landmark.onnx"):
    path = os.path.join(root, "assets", "models", name)
    sess = ort.InferenceSession(path, providers=["CPUExecutionProvider"])
    print(name)
    for i in sess.get_inputs():
        print("  in ", i.name, i.shape, i.type)
    for o in sess.get_outputs():
        print("  out", o.name, o.shape, o.type)

print("onnxruntime", ort.__version__)
capi = os.path.join(os.path.dirname(ort.__file__), "capi")
for f in sorted(glob.glob(os.path.join(capi, "*.dll"))):
    print("  dll", os.path.basename(f), os.path.getsize(f))

import mediapipe as mp

print("mediapipe", mp.__version__)
mods = os.path.join(os.path.dirname(mp.__file__), "modules")
for f in sorted(glob.glob(os.path.join(mods, "**", "*.tflite"), recursive=True)):
    if "hand" in f or "palm" in f:
        print("  tflite", os.path.relpath(f, mods), os.path.getsize(f))
print("python", sys.version.split()[0])

"""Run the official MediaPipe Hands pipeline on the test images and store its landmarks.

The Go pipeline is compared against this file (see cmd/visioncheck).
Usage: python tools/mp_reference.py
"""
import glob
import json
import os

import cv2
import mediapipe as mp

root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
images = sorted(glob.glob(os.path.join(root, "testdata", "images", "*.jpg")))
out = {}
with mp.solutions.hands.Hands(
    static_image_mode=True,
    max_num_hands=2,
    model_complexity=1,
    min_detection_confidence=0.5,
) as hands:
    for path in images:
        bgr = cv2.imread(path)
        h, w = bgr.shape[:2]
        res = hands.process(cv2.cvtColor(bgr, cv2.COLOR_BGR2RGB))
        entry = {"width": w, "height": h, "hands": []}
        if res.multi_hand_landmarks:
            for lms, handed in zip(res.multi_hand_landmarks, res.multi_handedness):
                c = handed.classification[0]
                entry["hands"].append(
                    {
                        "label": c.label,
                        "score": round(c.score, 4),
                        "landmarks": [[round(p.x * w, 2), round(p.y * h, 2), round(p.z * w, 2)] for p in lms.landmark],
                    }
                )
        name = os.path.basename(path)
        out[name] = entry
        print(name, w, h, [(x["label"], x["score"]) for x in entry["hands"]])

dst = os.path.join(root, "testdata", "reference")
os.makedirs(dst, exist_ok=True)
with open(os.path.join(dst, "mediapipe_landmarks.json"), "w", encoding="utf-8") as f:
    json.dump(out, f)
print("saved", len(out))

// Package assets embeds the neural network files into the executable.
//
// Both models are ONNX conversions of MediaPipe's hand models (Apache 2.0),
// taken from the OpenCV Zoo.
package assets

import _ "embed"

// PalmModel finds palms in a whole frame.
//
//go:embed models/palm_detection.onnx
var PalmModel []byte

// LandmarkModel returns 21 landmarks for one cropped hand.
//
//go:embed models/hand_landmark.onnx
var LandmarkModel []byte

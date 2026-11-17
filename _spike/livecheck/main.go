// Diagnostic only: compares still detection and continuous tracking on the same
// live camera frames. No input is injected and no images are saved.
package main

import (
 "fmt"
 "time"
 "gesturectl/assets"
 "gesturectl/internal/camera"
 "gesturectl/internal/gesture"
 "gesturectl/internal/vision"
)

func main(){
 lib,err:=vision.FindRuntimeLibrary(); if err!=nil { panic(err) }
 m,err:=vision.NewModels(vision.ModelOptions{LibraryPath:lib,PalmModel:assets.PalmModel,LandmarkModel:assets.LandmarkModel});if err!=nil {panic(err)};defer m.Close()
 cam,err:=camera.Open(camera.Config{Width:640,Height:480,FPS:30,Mirror:true});if err!=nil {panic(err)};defer cam.Close()
 tr:=vision.NewTracker(m,vision.DefaultTrackerConfig())
 ps,_:=gesture.NewPoseSet(gesture.BuiltinPoses());cfg:=gesture.DefaultConfig()
 r:=gesture.NewRecognizer(cfg,ps);r.Configure(cfg,ps,map[string]bool{"fist":true,"point":true,"two":true})
 r2:=gesture.NewRecognizer(cfg,ps);r2.Configure(cfg,ps,map[string]bool{"fist":true,"point":true,"two":true})
 var frame *vision.Frame;var seq uint64
 start:=time.Now();last:=time.Time{}
 for time.Since(start)<15*time.Second {
  f,s,stamp,e:=cam.Next(frame,seq,time.Second);if e!=nil {panic(e)};frame,seq=f,s
  h,_,e:=tr.Process(f,stamp);if e!=nil {panic(e)}
  states:=r.Update(h,stamp)
  if time.Since(last)<time.Second {continue};last=time.Now()
  still,e:=m.DetectStill(f,.6,.6,2);if e!=nil {panic(e)}
  ss:=r2.Update(still,stamp)
  fmt.Printf("%s TRACK ",time.Now().Format("15:04:05"))
  for n,st:=range states {if n<len(h){fmt.Printf("id%d %s raw=%s any=%s open=%.2v center=%.0f,%.0f size=%.0f roi=%+.0v presence=%.2f; ",st.ID,st.Side,st.RawPose,st.AnyPose,st.Features.Openness,st.Features.PalmX,st.Features.PalmY,st.Features.PalmSize,h[n].ROI,h[n].Presence)}}
  fmt.Printf("\n          STILL ")
  for _,st:=range ss {fmt.Printf("%s raw=%s any=%s open=%.2v center=%.0f,%.0f size=%.0f; ",st.Side,st.RawPose,st.AnyPose,st.Features.Openness,st.Features.PalmX,st.Features.PalmY,st.Features.PalmSize)}
  fmt.Println()
 }
}

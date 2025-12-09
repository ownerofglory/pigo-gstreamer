# Pigo + GStreamer demo

Demo app of using [Pigo library](https://github.com/esimov/pigo) for face recognition
with Gstreamer pipeline (no OpenCV needed)

## Run on MacOS
```shell
go run main.go \
  -width=640 \
  -height=480 \
  -pipeline 'avfvideosrc device-index=0 ! videoconvert ! videoscale ! video/x-raw,format=GRAY8,width=640,height=480,framerate=30/1 ! tee name=t t. ! queue ! videoconvert ! autovideosink sync=false t. ! queue max-size-buffers=1 leaky=downstream ! fdsink fd=1 sync=false'

```
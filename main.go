package main

import "C"

import (
	"bufio"
	"context"
	"flag"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	pigo "github.com/esimov/pigo/core"
)

var (
	classifier *pigo.Pigo
	cascade    []byte
)

// loadCascade loads and unpacks the Pigo cascade file.
func loadCascade(path string) *pigo.Pigo {
	if len(cascade) != 0 && classifier != nil {
		return classifier
	}

	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("Error reading the cascade file: %v", err)
	}
	cascade = data

	p := pigo.NewPigo()
	classifier, err = p.Unpack(cascade)
	if err != nil {
		log.Fatalf("Error unpacking the cascade file: %v", err)
	}
	return classifier
}

// detectFaces runs Pigo on a grayscale frame and returns detections.
func detectFaces(classifier *pigo.Pigo, pixels []uint8, rows, cols int) []pigo.Detection {
	cParams := pigo.CascadeParams{
		MinSize:     100,
		MaxSize:     600,
		ShiftFactor: 0.15,
		ScaleFactor: 1.1,
		ImageParams: pigo.ImageParams{
			Pixels: pixels,
			Rows:   rows,
			Cols:   cols,
			Dim:    cols,
		},
	}

	dets := classifier.RunCascade(cParams, 0.0)
	dets = classifier.ClusterDetections(dets, 0.0)
	return dets
}

// startGst starts a gst-launch-1.0 pipeline and returns cmd + stdout reader.
func startGst(ctx context.Context, pipeline string) (*exec.Cmd, io.ReadCloser, error) {
	args := append([]string{"-e"}, splitArgs(pipeline)...)
	cmd := exec.CommandContext(ctx, "gst-launch-1.0", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, nil, err
	}
	log.Printf("Started gst-launch-1.0 with args: %v", cmd.Args)
	return cmd, stdout, nil
}

// splitArgs is a minimal whitespace splitter (no full shell parsing).
func splitArgs(s string) []string {
	var args []string
	current := ""
	inQuotes := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case ' ':
			if inQuotes {
				current += string(ch)
			} else if current != "" {
				args = append(args, current)
				current = ""
			}
		case '"':
			inQuotes = !inQuotes
		default:
			current += string(ch)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	// --- Flags ---
	width := flag.Int("width", 640, "frame width (pixels)")
	height := flag.Int("height", 480, "frame height (pixels)")
	cascadePath := flag.String("cascade", "cascade/facefinder", "path to Pigo cascade file")

	// for macOS webcam:
	//   avfvideosrc device-index=0 ! videoconvert ! videoscale !
	//   video/x-raw,format=GRAY8,width=640,height=480,framerate=30/1 ! fdsink fd=1 sync=false
	//
	//  for RTP/H264:
	//   udpsrc port=5000 caps="application/x-rtp, media=video, encoding-name=H264, payload=96" !
	//   rtph264depay ! h264parse ! avdec_h264 !
	//   videoconvert ! videoscale !
	//   video/x-raw,format=GRAY8,width=640,height=480,framerate=30/1 ! fdsink fd=1 sync=false
	pipeline := flag.String("pipeline", "", "GStreamer pipeline (ending in GRAY8 video/x-raw to fdsink fd=1)")
	minScore := flag.Float64("min-score", 5.0, "minimum detection score (Q) to report")

	flag.Parse()

	if *pipeline == "" {
		log.Fatal("You must pass -pipeline with a valid GStreamer pipeline")
	}

	// --- Context + signals ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigs
		log.Println("Received signal, shutting down...")
		cancel()
	}()

	// --- Load Pigo cascade ---
	clf := loadCascade(*cascadePath)
	log.Println("Loaded Pigo cascade from", *cascadePath)

	// --- Start GStreamer pipeline ---
	cmd, stdout, err := startGst(ctx, *pipeline)
	if err != nil {
		log.Fatalf("failed to start GStreamer: %v", err)
	}
	defer func() {
		_ = stdout.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	reader := bufio.NewReader(stdout)

	frameSize := (*width) * (*height) // GRAY8: 1 byte per pixel
	buf := make([]byte, frameSize)

	log.Printf("Expecting GRAY8 frames of %dx%d (%d bytes)\n", *width, *height, frameSize)

	frameCount := 0
	startTime := time.Now()

	for {
		if ctx.Err() != nil {
			log.Println("Context canceled, stopping main loop")
			break
		}

		_, err := io.ReadFull(reader, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				log.Println("GStreamer pipeline ended")
				break
			}
			log.Fatalf("error reading frame from GStreamer: %v", err)
		}

		frameCount++

		// Detect faces
		dets := detectFaces(clf, buf, *height, *width)

		for _, det := range dets {
			if det.Q >= float32(*minScore) {
				// Pigo returns Row, Col, Scale, Q
				log.Printf("frame=%d face row=%d col=%d scale=%d q=%.2f",
					frameCount, det.Row, det.Col, det.Scale, det.Q)
			}
		}

		// Simple FPS report every 60 frames
		if frameCount%60 == 0 {
			elapsed := time.Since(startTime).Seconds()
			fps := float64(frameCount) / elapsed
			log.Printf("Processed %d frames (%.1f FPS)", frameCount, fps)
		}
	}

	log.Println("Exiting.")
}

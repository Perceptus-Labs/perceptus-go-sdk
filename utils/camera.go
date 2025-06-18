package utils

import (
	"fmt"
	"os/exec"
	"runtime"

	log "github.com/sirupsen/logrus"
)

type CameraCapture struct {
	DeviceID int
}

func NewCameraCapture() *CameraCapture {
	return &CameraCapture{
		DeviceID: 0, // Default camera device
	}
}

// CaptureImage captures an image from the camera and returns the image data as bytes
func (c *CameraCapture) CaptureImage() ([]byte, error) {
	var cmd *exec.Cmd

	// Different commands based on operating system
	switch runtime.GOOS {
	case "darwin": // macOS
		// Use ffmpeg to capture from camera (requires ffmpeg to be installed)
		cmd = exec.Command("ffmpeg", "-f", "avfoundation", "-video_size", "640x480", "-framerate", "30", "-i", fmt.Sprintf("%d", c.DeviceID), "-vframes", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-")
	case "linux":
		// Use ffmpeg with v4l2 (Video4Linux2) on Linux
		cmd = exec.Command("ffmpeg", "-f", "v4l2", "-video_size", "640x480", "-i", fmt.Sprintf("/dev/video%d", c.DeviceID), "-vframes", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-")
	case "windows":
		// Use ffmpeg with dshow (DirectShow) on Windows
		cmd = exec.Command("ffmpeg", "-f", "dshow", "-video_size", "640x480", "-i", fmt.Sprintf("video=\"USB Camera\""), "-vframes", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "-")
	default:
		return nil, fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// Execute the command and capture output
	output, err := cmd.Output()
	if err != nil {
		log.WithError(err).Error("Failed to capture image from camera")
		return nil, fmt.Errorf("failed to capture image: %w", err)
	}

	if len(output) == 0 {
		return nil, fmt.Errorf("no image data captured")
	}

	log.Debug("Successfully captured image", "size", len(output))
	return output, nil
}

// Alternative method using imagesnap on macOS (if available)
func (c *CameraCapture) CaptureImageMacOS() ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("imagesnap is only available on macOS")
	}

	// Use imagesnap to capture image to stdout
	cmd := exec.Command("imagesnap", "-")
	output, err := cmd.Output()
	if err != nil {
		log.WithError(err).Error("Failed to capture image using imagesnap")
		return nil, fmt.Errorf("failed to capture image with imagesnap: %w", err)
	}

	if len(output) == 0 {
		return nil, fmt.Errorf("no image data captured")
	}

	log.Debug("Successfully captured image using imagesnap", "size", len(output))
	return output, nil
}

// TryCapture attempts to capture an image using the best available method
func (c *CameraCapture) TryCapture() ([]byte, error) {
	// First try the primary method
	data, err := c.CaptureImage()
	if err == nil {
		return data, nil
	}

	log.WithError(err).Warn("Primary capture method failed, trying alternatives")

	// On macOS, try imagesnap as an alternative
	if runtime.GOOS == "darwin" {
		data, err := c.CaptureImageMacOS()
		if err == nil {
			return data, nil
		}
		log.WithError(err).Warn("Alternative capture method also failed")
	}

	return nil, fmt.Errorf("all capture methods failed")
}

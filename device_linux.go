package tpm2

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

const (
	maxCommandSize int = 4096
)

type tctiDeviceLinux struct {
	f *os.File
	buf *bytes.Reader
}

func (d *tctiDeviceLinux) readMoreData() error {
	fds := []unix.PollFd{unix.PollFd{Fd: int32(d.f.Fd()), Events: unix.POLLIN}}
	_, err := unix.Ppoll(fds, nil, nil)
	if err != nil {
		return fmt.Errorf("polling device failed: %v", err)
	}

	if fds[0].Events != fds[0].Revents {
		return fmt.Errorf("invalid poll events returned: %d", fds[0].Revents)
	}

	buf := make([]byte, maxCommandSize)
	n, err := d.f.Read(buf)
	if err != nil {
		return fmt.Errorf("reading from device failed: %v", err)
	}

	d.buf = bytes.NewReader(buf[:n])
	return nil
}

func (d *tctiDeviceLinux) Read(data []byte) (int, error) {
	if d.buf == nil || d.buf.Len() == 0 {
		if err := d.readMoreData(); err != nil {
			return 0, err
		}
	}

	return d.buf.Read(data)
}

func (d *tctiDeviceLinux) Write(data []byte) (int, error) {
	return d.f.Write(data)
}

func (d *tctiDeviceLinux) Close() error {
	return d.f.Close()
}

func OpenTPMDevice(path string) (io.ReadWriteCloser, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("cannot open linux TPM device: %v", err)
	}

	s, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot stat linux TPM device: %v", err)
	}

	if s.Mode()&os.ModeDevice == 0 {
		return nil, fmt.Errorf("unsupported file mode %v", s.Mode())
	}

	return &tctiDeviceLinux{f: f}, nil
}

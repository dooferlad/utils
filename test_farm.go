package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"regexp"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// RemoteWorker is all the information we need to maintain a connection to a
// remote machine over SSH.
type RemoteWorker struct {
	reader         *bufio.Reader
	stdin          io.WriteCloser
	promptMatch    *regexp.Regexp
	ssh_agent_conn net.Conn
	conn           *ssh.Client
	ag             agent.Agent
	config         *ssh.ClientConfig
	session        *ssh.Session
	stdout         io.Reader
	wg             *sync.WaitGroup
}

// waitForPrompt grabs text up to a $ then checks to see if that matched a
// prompt using the regexp stored in RemoteWorker.promptMatch. If it doesn't
// match the prompt it saves the text that it has so far and gets text up to
// the next $
func (r *RemoteWorker) waitForPrompt() string {
	var line string
	for {
		chunk, _ := r.reader.ReadString('$')
		//fmt.Printf(chunk)
		line += chunk
		if matched := r.promptMatch.FindStringSubmatch(line); matched != nil {
			//fmt.Printf("\n")
			return matched[1]
		}
	}
}

// Send a command string, append a newline so it is executed
func (r *RemoteWorker) remoteCommand(command string) {
	fmt.Println(command)
	r.stdin.Write([]byte(command + "\n"))
}

// Setup initiates the SSH connection to a host and sets up the regular
// expression to match the prompt.
func (r *RemoteWorker) Setup(host string, wg *sync.WaitGroup) {
	current_user, _ := user.Current()
	username := current_user.Username
	r.wg = wg

	var err error
	r.ssh_agent_conn, err = net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		log.Fatal(err)
	}
	r.ag = agent.NewClient(r.ssh_agent_conn)
	auths := []ssh.AuthMethod{ssh.PublicKeysCallback(r.ag.Signers)}

	// Define the Client Config as :
	r.config = &ssh.ClientConfig{
		User: username,
		Auth: auths,
	}

	// Connect to ssh server
	r.conn, err = ssh.Dial("tcp", host+":22", r.config)
	if err != nil {
		log.Fatalf("unable to connect: %s", err)
	}
	// Create a session
	r.session, err = r.conn.NewSession()
	if err != nil {
		log.Fatalf("unable to create session: %s", err)
	}
	// Set up terminal modes
	modes := ssh.TerminalModes{
		ssh.ECHO:          0,     // disable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	r.stdout, err = r.session.StdoutPipe()
	if err != nil {
		log.Fatalf("unable to acquire stdout pipe: %s", err)
	}

	r.stdin, err = r.session.StdinPipe()
	if err != nil {
		log.Fatalf("unable to acquire stdin pipe: %s", err)
	}

	// Request pseudo terminal
	if err := r.session.RequestPty("xterm", 80, 40, modes); err != nil {
		log.Fatalf("request for pseudo terminal failed: %s", err)
	}
	// Start remote shell
	if err := r.session.Shell(); err != nil {
		log.Fatalf("failed to start shell: %s", err)
	}

	r.reader = bufio.NewReader(r.stdout)

	re := fmt.Sprintf("(?s)(^.*)%s@%s:.*\\$", username, host)
	r.promptMatch, _ = regexp.Compile(re)
	r.waitForPrompt()
}

// Close gracefully terminates the SSH connection and connection to the local
// SSH agent.
func (r *RemoteWorker) Close() {
	r.ssh_agent_conn.Close()
	r.conn.Close()
	r.session.Close()
}

// Test a single juju package
func (r *RemoteWorker) TestPackage(pkg string) string {

	r.remoteCommand("cd ~/dev/go/src/github.com/juju/juju/")
	r.waitForPrompt()
	r.remoteCommand("cd " + pkg)
	r.waitForPrompt()
	r.remoteCommand("go test -test.timeout=1200s ./...")
	line := r.waitForPrompt()
	return line
}

// TestPackages receives names of packages to test on package_chan and returns
// their output on results_chan. Once there are no more packages to test it
// closes the SSH connection and signals that it is done on the wait group
// RemoteWorker.wg
func (r *RemoteWorker) TestPackages(package_chan, results_chan chan string) {
	for pkg := range package_chan {
		results_chan <- r.TestPackage(pkg)
	}
	r.Close()
	r.wg.Done()
}

func main() {
	var packages = []string{"apiserver", "worker", "cmd", "replicaset",
		"state", "api", "environs", "provider", "upgrades", "juju",
		"featuretests", "bzr", "container", "downloader", "testing",
		"cert", "agent", "storage", "mongo", "cloudinit", "lease",
		"utils", "rpc", "service", "network", "version", "constraints",
		"instance", "leadership", "audit", "tools"}

	package_chan := make(chan string, len(packages))
	results_chan := make(chan string, len(packages))

	var wg sync.WaitGroup

	for i := range packages {
		package_chan <- packages[len(packages)-1-i]
	}
	close(package_chan)

	var worker_names = []string{"homework1", "homework2", "homework4"}
	var workers = []RemoteWorker{}

	for _, name := range worker_names {
		w := RemoteWorker{}
		wg.Add(1)
		w.Setup(name, &wg)
		go w.TestPackages(package_chan, results_chan)
		workers = append(workers, w)
	}

	for got_results := 0; got_results < len(packages); got_results++ {
		fmt.Print(<-results_chan)
	}

	wg.Wait()
}

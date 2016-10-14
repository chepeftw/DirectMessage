package main
 
import (
    "os"
    "fmt"
    "net"
    "time"
    "bufio"
    "regexp"
    "strconv"
    "strings"
    "os/exec"
    "math/rand"
    "encoding/json"

    "github.com/op/go-logging"
)


// +++++++++++++++++++++++++++
// +++++++++ Go-Logging Conf
// +++++++++++++++++++++++++++
var log = logging.MustGetLogger("treesip")

var format = logging.MustStringFormatter(
    "%{level:.4s}** %{time:0102 15:04:05.999999} %{pid} %{shortfile} %{message}",
)


// +++++++++ Constants
const (
    Port              = ":10001"
    Protocol          = "udp"
    BroadcastAddr     = "255.255.255.255"
)

const (
    NONE = iota
    HELLO
    HELLO_REPLY
    ROUTE
)

// +++++++++ Global vars
var myIP net.IP = net.ParseIP("127.0.0.1")

var routes map[string]string = make(map[string]string)
var globalTimestamp = 0
// var RouterWaitRoom []Packet = []Packet{}
var RouterWaitRoom map[string]Packet = make(map[string]Packet)
var ForwardedMessages []string = []string{}

// +++++++++ Channels
var buffer = make(chan string)
var router = make(chan string)
var output = make(chan string)
var done = make(chan bool)

// +++++++++ Packet structure
type Packet struct {
    Type         int        `json:"type,omitempty"`
    Message      string     `json:"message,omitempty"`
    Source       net.IP     `json:"source,omitempty"`
    Destination  net.IP     `json:"destination,omitempty"`
    Gateway      net.IP     `json:"gateway,omitempty"`
    Timestamp    string     `json:"timestamp,omitempty"`
}

 
// A Simple function to verify error
func CheckError(err error) {
    if err  != nil {
        log.Error("Error: ", err)
    }
}

// Getting my own IP, first we get all interfaces, then we iterate
// discard the loopback and get the IPv4 address, which should be the eth0
func SelfIP() net.IP {
    addrs, err := net.InterfaceAddrs()
    if err != nil {
        panic(err)
    }

    for _, a := range addrs {
        if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
            if ipnet.IP.To4() != nil {
                return ipnet.IP
            }
        }
    }

    return net.ParseIP("127.0.0.1")
}

func contains(s []string, e string) bool {
    for _, a := range s {
        if a == e {
            return true
        }
    }
    return false
}

// Function that handles the buffer channel
func attendBufferChannel() {
    s1 := rand.NewSource(time.Now().UnixNano())
    r1 := rand.New(s1)
    i := 0

    for {
        j, more := <-buffer
        if more {
            // First we take the json, unmarshal it to an object
            packet := Packet{}
            json.Unmarshal([]byte(j), &packet)

            if packet.Type == HELLO {
                if myIP.String() != packet.Source.String() {
                    if contains(ForwardedMessages, packet.Timestamp) {
                        time.Sleep(time.Duration(r1.Intn(25000)/100) * time.Millisecond)   
                    }
                    SendHelloReply(packet)
                }
            } else if packet.Type == HELLO_REPLY {
                if myIP.String() == packet.Destination.String() {
                    log.Info(myIP.String() + " HEELO_REPLY from " + packet.Source.String())                        
                    router <- "ADD|" + packet.Timestamp + " " + packet.Source.String()
                }
            } else if packet.Type == ROUTE {
                if myIP.String() == packet.Gateway.String() {
                    if myIP.String() == packet.Destination.String() {
                        i++
                        log.Info(myIP.String() + " SUCCESS ROUTE -> Timestamp: " + packet.Timestamp + " Message: " + packet.Message + " from " + packet.Source.String() + " => " + string(i))                        
                    } else {
                        log.Info(myIP.String() + " ++++++++++++++++ ROUTE -> Message: " + packet.Message + " from " + packet.Source.String())                        
                        router <- "ROUTE|" + j
                    }
                }
            }

            log.Info(myIP.String() + " -> Message: " + packet.Message + " From " + packet.Source.String())
        } else {
            fmt.Println("closing channel")
            done <- true
            return
        }
    }
}

func SendHello(packet Packet) {
    log.Info("Sending Hello")

    payload := Packet{
        Type: HELLO,
        Source: myIP,
        Timestamp: packet.Timestamp,
    }

    js, err := json.Marshal(payload)
    CheckError(err)

    output <- string(js)
}

func SendHelloReply(packet Packet) {
    payload := Packet{
        Type: HELLO_REPLY,
        Source: myIP,
        Destination: packet.Source,
        Timestamp: packet.Timestamp,
    }

    js, err := json.Marshal(payload)
    CheckError(err)

    output <- string(js)
}

func SendRoute(gateway net.IP, packet Packet) {
    payload := Packet{
        Type: ROUTE,
        Message: packet.Message,
        Source: packet.Source,
        Destination: packet.Destination,
        Gateway: gateway,
        Timestamp: packet.Timestamp,
    }

    js, err := json.Marshal(payload)
    CheckError(err)

    output <- string(js)
}

// Function that handles the router channel
func attendRouterChannel() {
    for {
        j, more := <-router
        if more {
            s := strings.Split(j, "|")
            opType, predicate := s[0], s[1]

            if opType == "ADD" {
                s1 := strings.Split(predicate, " ")
                stamp, dest := s1[0], s1[1]

                relaySelection := net.ParseIP(dest)

                if len(RouterWaitRoom) > 0 {
                    if _, ok := RouterWaitRoom[stamp]; ok {
                        SendRoute(relaySelection, RouterWaitRoom[stamp])
                        ForwardedMessages = append(ForwardedMessages, stamp)
                        if len(ForwardedMessages) > 100 {
                            ForwardedMessages = ForwardedMessages[len(ForwardedMessages)-100:]
                        }
                        delete(RouterWaitRoom, stamp)
                    }
                }
            } else if opType == "ROUTE" {
                log.Info("Attending router ROUTE")
                packet := Packet{}
                json.Unmarshal([]byte(predicate), &packet)
                RouterWaitRoom[packet.Timestamp] = packet
                SendHello(packet)
            }

        } else {
            fmt.Println("closing channel")
            done <- true
            return
        }
    }
}

// Function that handles the output channel
func attendOutputChannel() {
    ServerAddr,err := net.ResolveUDPAddr(Protocol, BroadcastAddr+Port)
    CheckError(err)
    LocalAddr, err := net.ResolveUDPAddr(Protocol, myIP.String()+":0")
    CheckError(err)
    Conn, err := net.DialUDP(Protocol, LocalAddr, ServerAddr)
    CheckError(err)
    defer Conn.Close()

    for {
        j, more := <-output
        if more {
            if Conn != nil {
                buf := []byte(j)
                _,err = Conn.Write(buf)
                CheckError(err)
            }
        } else {
            fmt.Println("closing channel")
            done <- true
            return
        }
    }
}

func beacon() {
    s1 := rand.NewSource(time.Now().UnixNano())
    r1 := rand.New(s1)
    t := strconv.Itoa(r1.Intn(100000))

    payload := Packet{
        Type: NONE,
        Message: "Hello network! "+t,
        Source: myIP,
    }

    jsByte, err := json.Marshal(payload)
    CheckError(err)
    js := string(jsByte)

    log.Info("Our random message is "+t)

    for {
        output <- js
        time.Sleep(time.Duration(5 + r1.Intn(18)) * time.Second)
    }
}

func parseRoutes() {
    fmt.Println("Starting parseRoutes()")
    for {
        out, err := exec.Command("route", "-n").Output()
        CheckError(err)

        scanner := bufio.NewScanner(strings.NewReader(string(out[:])))

        i := 0
        for scanner.Scan() {
            if i < 2 {
                i++
                continue
            }

            s := scanner.Text()
            // fmt.Println(s) // Println will add back the final '\n'

            re_leadclose_whtsp := regexp.MustCompile(`^[\s\p{Zs}]+|[\s\p{Zs}]+$`)
            re_inside_whtsp := regexp.MustCompile(`[\s\p{Zs}]{2,}`)
            final := re_leadclose_whtsp.ReplaceAllString(s, "")
            final = re_inside_whtsp.ReplaceAllString(final, " ")

            arr := strings.Split(final, " ")
            // fmt.Println("Destination: %s - Gateway: %s", arr[0], arr[1])

            router <- "ADD|" + arr[0] + " " + arr[1]
        }

        if err := scanner.Err(); err != nil {
            fmt.Fprintln(os.Stderr, "reading standard input:", err)
        }

        time.Sleep(time.Second * 1)
    }
}

func sendAwesomeMessage() {
    if "10.12.0.25" == myIP.String() {
        i := 0
        for {
            log.Info("Waiting to SEND Awesome message to 10.12.0.1")
            time.Sleep(time.Second * 10)
            i++
            log.Info("Sending Awesome message to 10.12.0.1")
            payload := Packet{
                Type: ROUTE,
                Message: "ROUTING!",
                Source: myIP,
                Destination: net.ParseIP("10.12.0.1"),
                Gateway: myIP,
                Timestamp: strings.Replace(myIP.String(), ".", "", -1) + "_" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10),
            }

            log.Info("Payload.Timestamp: " + payload.Timestamp + " => " + string(i))

            js, err := json.Marshal(payload)
            CheckError(err)

            // output <- js
            buffer <- string(js)
        }
    }
}
 
func main() {
    fmt.Printf("Hello World!")

    // +++++++++++++++++++++++++++++
    // ++++++++ Logger conf
    var logPath = "/var/log/golang/"
    if _, err := os.Stat(logPath); os.IsNotExist(err) {
        os.MkdirAll(logPath, 0777)
    }

    var logFile = logPath + "treesip.log"
    f, err := os.OpenFile(logFile, os.O_APPEND | os.O_CREATE | os.O_RDWR, 0666)
    if err != nil {
        fmt.Printf("error opening file: %v", err)
    }

    // don't forget to close it
    defer f.Close()

    backend := logging.NewLogBackend(f, "", 0)
    backendFormatter := logging.NewBackendFormatter(backend, format)

    logging.SetBackend(backendFormatter)
    // ++++++++ END Logger conf
    // +++++++++++++++++++++++++++++

    log.Info("Waiting for UPD Beacon")

    // It gives one minute time for the network to get configured before it gets its own IP.
    time.Sleep(time.Second * 30)
    myIP = SelfIP();

    log.Info("Starting UPD Beacon")

    // Lets prepare a address at any address at port 10001
    ServerAddr,err := net.ResolveUDPAddr(Protocol, Port)
    CheckError(err)
 
    // Now listen at selected port
    ServerConn, err := net.ListenUDP(Protocol, ServerAddr)
    CheckError(err)
    defer ServerConn.Close()

    go attendBufferChannel()
    go attendRouterChannel()
    go attendOutputChannel()
    // go beacon()
    // go parseRoutes()
    go sendAwesomeMessage()

    buf := make([]byte, 1024)
 
    for {
        n,_,err := ServerConn.ReadFromUDP(buf)
        buffer <- string(buf[0:n])
        
        if err != nil {
            log.Error("Error: ",err)
        }
    }

    close(buffer)

    <-done
}
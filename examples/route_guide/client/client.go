/*
 *
 * Copyright 2015 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

// Package main implements a simple gRPC client that demonstrates how to use gRPC-Go libraries
// to perform unary, client streaming, server streaming and full duplex RPCs.
//
// It interacts with the route guide service whose definition can be found in routeguide/route_guide.proto.
package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"google.golang.org/grpc/keepalive"
	"io"
	"log"
	"math"
	"math/rand"
	"time"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	pb "google.golang.org/grpc/examples/route_guide/routeguide"
	"google.golang.org/grpc/testdata"
	"net"
	"syscall"
)

var (
	tls                = flag.Bool("tls", false, "Connection uses TLS if true, else plain TCP")
	caFile             = flag.String("ca_file", "", "The file containing the CA root cert file")
	serverAddr         = flag.String("server_addr", "localhost:10000", "The server address in the format of host:port")
	serverHostOverride = flag.String("server_host_override", "x.test.youtube.com", "The server name used to verify the hostname returned by the TLS handshake")
)

// printFeature gets the feature for the given point.
func printFeature(client pb.RouteGuideClient, point *pb.Point) {
	log.Printf("Getting feature for point (%d, %d)", point.Latitude, point.Longitude)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	feature, err := client.GetFeature(ctx, point)
	if err != nil {
		log.Fatalf("%v.GetFeatures(_) = _, %v: ", client, err)
	}
	log.Println(feature)
}

// printFeatures lists all the features within the given bounding Rectangle.
func printFeatures(client pb.RouteGuideClient, rect *pb.Rectangle) {
	log.Printf("Looking for features within %v", rect)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.ListFeatures(ctx, rect)
	if err != nil {
		log.Fatalf("%v.ListFeatures(_) = _, %v", client, err)
	}
	for {
		feature, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatalf("%v.ListFeatures(_) = _, %v", client, err)
		}
		log.Println(feature)
	}
}

// runRecordRoute sends a sequence of points to server and expects to get a RouteSummary from server.
func runRecordRoute(client pb.RouteGuideClient) {
	// Create a random number of random points
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	pointCount := int(r.Int31n(100)) + 2 // Traverse at least two points
	var points []*pb.Point
	for i := 0; i < pointCount; i++ {
		points = append(points, randomPoint(r))
	}
	log.Printf("Traversing %d points.", len(points))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.RecordRoute(ctx)
	if err != nil {
		log.Fatalf("%v.RecordRoute(_) = _, %v", client, err)
	}
	for _, point := range points {
		if err := stream.Send(point); err != nil {
			log.Fatalf("%v.Send(%v) = %v", stream, point, err)
		}
	}
	reply, err := stream.CloseAndRecv()
	if err != nil {
		log.Fatalf("%v.CloseAndRecv() got error %v, want %v", stream, err, nil)
	}
	log.Printf("Route summary: %v", reply)
}

// readUint32 reads an integer from an io.Reader (which should be a CSPRNG)
func readUint32(rng io.Reader) uint32 {
	var rndBytes [4]byte
	i, err := rng.Read(rndBytes[:])
	if i != 4 || err != nil {
		panic(fmt.Sprintf("cannot read from rng: %+v", err))
	}
	return binary.BigEndian.Uint32(rndBytes[:])
}

// readRangeUint32 reduces an integer from 0, MaxUint32 to the range start, end
func readRangeUint32(start, end uint32, rng io.Reader) uint32 {
	size := end - start
	// note we could just do the part inside the () here, but then extra
	// can == size which means a little bit of range is wastes, either
	// choice seems negligible so we went with the "more correct"
	extra := (math.MaxUint32%size + 1) % size
	limit := math.MaxUint32 - extra
	// Loop until we read something inside the limit
	for {
		res := readUint32(rng)
		if res > limit {
			continue
		}
		return (res % size) + start
	}
}

func genRandString(size int) string {
	alphabet := string("abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*()")
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	res := make([]byte, size)
	for i := 0; i < size; i++ {
		res[i] = alphabet[readRangeUint32(0, uint32(len(alphabet)), rng)]
	}
	return string(res)
}

// runRouteChat receives a sequence of route notes, while sending notes for various locations.
func runRouteChat(client pb.RouteGuideClient) {
	numNotes := 1024
	notes := make([]*pb.RouteNote, numNotes)
	for i := 0; i < numNotes; i++ {
		notes[i] = &pb.RouteNote{
			Location: &pb.Point{
				Latitude:  0 + int32(i),
				Longitude: 1 + int32(i),
			},
			Message: genRandString(1024),
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := client.RouteChat(ctx)
	if err != nil {
		log.Fatalf("%v.RouteChat(_) = _, %v", client, err)
	}
	waitc := make(chan struct{})
	go func() {
		for {
			in, err := stream.Recv()
			if err == io.EOF {
				// read done.
				close(waitc)
				return
			}
			if err != nil {
				log.Fatalf("Failed to receive a note : %v", err)
			}
			log.Printf("Got message %s at point(%d, %d)", in.Message, in.Location.Latitude, in.Location.Longitude)
		}
	}()
	for _, note := range notes {
		if err := stream.Send(note); err != nil {
			log.Fatalf("Failed to send a note: %v", err)
		}
	}
	stream.CloseSend()
	<-waitc
}

func randomPoint(r *rand.Rand) *pb.Point {
	lat := (r.Int31n(180) - 90) * 1e7
	long := (r.Int31n(360) - 180) * 1e7
	return &pb.Point{Latitude: lat, Longitude: long}
}

const infinityTime = time.Duration(math.MaxInt64)

// KaClientOpts are the keepalive options for clients
// TODO: Set via configuration
var KaClientOpts = keepalive.ClientParameters{
	// Never ping to keepalive
	Time: infinityTime,
	// 60s after ping before closing
	Timeout: 60 * time.Minute,
	// For all connections, with and without streaming
	PermitWithoutStream: true,
}

func TestCustomDialerOption(ctx context.Context, addr string) (net.Conn, error) {
	control := func(network, address string, c syscall.RawConn) error {
		fmt.Printf("DOING THING 1\n")
		var syscallErr error
		controlErr := c.Control(func(fd uintptr) {
			syscallErr = syscall.SetsockoptInt(
				int(fd), syscall.IPPROTO_TCP,
				unix.SO_RCVBUF,
				8*1024*1024)
		})
		if syscallErr != nil {
			fmt.Printf("syscall: %+v\n", syscallErr)
			return syscallErr
		}
		if controlErr != nil {
			fmt.Printf("control: %+v\n", controlErr)
			return controlErr
		}
		fmt.Printf("DOING THING 2\n")
		controlErr = c.Control(func(fd uintptr) {
			syscallErr = syscall.SetsockoptInt(
				int(fd), syscall.SOL_SOCKET,
				unix.SO_SNDBUF,
				8*1024*1024)
		})
		if syscallErr != nil {
			fmt.Printf("syscall: %+v\n", syscallErr)
			return syscallErr
		}
		if controlErr != nil {
			fmt.Printf("control: %+v\n", controlErr)
			return controlErr
		}
		fmt.Printf("DOING THING 3\n")
		controlErr = c.Control(func(fd uintptr) {
			syscallErr = syscall.SetsockoptInt(
				int(fd), syscall.IPPROTO_TCP,
				unix.TCP_WINDOW_CLAMP,
				4*1024*1024)
		})
		if syscallErr != nil {
			fmt.Printf("syscall %+v\n", syscallErr)
			return syscallErr
		}
		if controlErr != nil {
			fmt.Printf("control %+v\n", controlErr)
			return controlErr
		}
		return nil
	}
	d := &net.Dialer{
		Control: control,
	}
	return d.DialContext(ctx, "tcp", addr)
}

func main() {
	flag.Parse()
	var opts []grpc.DialOption
	if *tls {
		if *caFile == "" {
			*caFile = testdata.Path("ca.pem")
		}
		creds, err := credentials.NewClientTLSFromFile(*caFile, *serverHostOverride)
		if err != nil {
			log.Fatalf("Failed to create TLS credentials %v", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithInsecure())
	}

	opts = append(opts, grpc.WithBlock(),
		grpc.WithContextDialer(TestCustomDialerOption))
	// opts = append(opts, grpc.WithKeepaliveParams(KaClientOpts))
	// opts = append(opts, grpc.WithReadBufferSize(4*1024*1024))
	// opts = append(opts, grpc.WithWriteBufferSize(4*1024*1024))
	// opts = append(opts, grpc.WithInitialWindowSize(10*1024*1024))
	// opts = append(opts, grpc.WithInitialConnWindowSize(10*1024*1024))
	conn, err := grpc.Dial(*serverAddr, opts...)
	if err != nil {
		log.Fatalf("fail to dial: %v", err)
	}
	defer conn.Close()

	client := pb.NewRouteGuideClient(conn)

	// Looking for a valid feature
	printFeature(client, &pb.Point{Latitude: 409146138, Longitude: -746188906})

	// Feature missing.
	printFeature(client, &pb.Point{Latitude: 0, Longitude: 0})

	// Looking for features between 40, -75 and 42, -73.
	printFeatures(client, &pb.Rectangle{
		Lo: &pb.Point{Latitude: 400000000, Longitude: -750000000},
		Hi: &pb.Point{Latitude: 420000000, Longitude: -730000000},
	})

	// // RecordRoute
	// runRecordRoute(client)

	// RouteChat
	for i := 0; i < 100; i++ {
		runRouteChat(client)
		time.Sleep(7 * time.Second)
	}
}

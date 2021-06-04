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

// Package main implements a client for Greeter service.
package main

import (
	"context"
	"log"
	"os"
	"time"
	"google.golang.org/grpc/keepalive"

	"google.golang.org/grpc"
	pb "google.golang.org/grpc/examples/helloworld/helloworld"
)

const (
	address     = "localhost:50051"
	defaultName = "world"
)

// KaClientOpts are the keepalive options for clients
// TODO: Set via configuration
var KaClientOpts = keepalive.ClientParameters{
	// Wait 1s before pinging to keepalive
	Time: 1 * time.Second,
	// 2s after ping before closing
	Timeout: 2 * time.Second,
	// For all connections, streaming and nonstreaming
	PermitWithoutStream: true,
}


func sendmsg() {
	// Set up a connection to the server.
	ctx, cancel := context.WithTimeout(context.Background(),
		10*time.Second)

	conn, err := grpc.DialContext(ctx, address, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithKeepaliveParams(KaClientOpts))
	if err != nil {
		log.Fatalf("did not connect: %v", err)
	}
	cancel()
	defer conn.Close()
	for i := 0; i < 5; i++ {
		go func() {
			c := pb.NewGreeterClient(conn)

			// Contact the server and print out its response.
			name := defaultName
			if len(os.Args) > 1 {
				name = os.Args[1]
			}
			ctx, cancel = context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			r, err := c.SayHello(ctx, &pb.HelloRequest{Name: name})
			if err != nil {
				log.Fatalf("could not greet: %v", err)
			}
			log.Printf("Greeting: %s", r.GetMessage())
		}
		time.Sleep(5*time.Second)
	}
}


func main() {
	for {
		go sendmsg()
		time.Sleep(5*time.Second)
	}
}

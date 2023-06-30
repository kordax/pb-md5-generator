#!/usr/bin/bash

protoc --proto_path=./test-proto/ --go_opt=Mtest_proto=./test-proto --descriptor_set_out=test-proto/test.pb.desc --include_source_info test_proto --go_out=./

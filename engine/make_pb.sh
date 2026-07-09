#!/usr/bin/bash

protoc --proto_path=../testdata/test-proto/ --go_opt=Mtest_proto=../testdata/test-proto --descriptor_set_out=../testdata/test-proto/test.pb.desc --include_source_info test_proto --go_out=../

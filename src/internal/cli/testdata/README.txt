Golden files in this directory hold the expected CLI output for the cases in golden_test.go

The files are compared byte for byte by go test, and only the local -update flag rewrites them

本目录下的 golden 文件保存 golden_test.go 各用例的期望输出

go test 逐字节比较这些文件, 只有本地 -update 开关会重写它们

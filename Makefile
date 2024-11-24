.PHONY: run_local
run_local:
	go build main.go && ./main run --config ./config.yml && rm -rf ./main

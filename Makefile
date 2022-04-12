docker:
	@docker-compose down
	@docker-compose -f "docker-compose.yml" up --build --remove-orphans --force-recreate --exit-code-from consumer

dockerdown:
	@docker-compose down

lint:
	@golangci-lint run

run-build:
	@echo "---- Building Agent from cmd/bspagent ----"
	@go build -o ./bin/bspagent/agent ./cmd/bspagent/*.go 
	@echo "---- Done Building to ./bin/agent ----"

run-agent-eth:
	@echo "---- Running Agent from cmd/bspagent ----"
	@go run ./cmd/bspagent/*.go \
	--redis-url="redis://username:@localhost:6379/0?topic=replication-2#replicate-3" \
	--avro-codec-path="./codec/block-ethereum.avsc" \
	--binary-file-path="./bin/block-ethereum/" \
	--gcp-svc-account="/Users/pranay/.config/gcloud/bsp-2.json" \
	--replica-bucket="covalenthq-geth-block-specimen" \
	--segment-length=1 \
	--proof-chain-address=0x652494F4726106Ed47e508b41c86a82F8a854a71 \
	--consumer-timeout=80

run-agent-elrond:
	@echo "---- Running Agent from cmd/bspagent ----"
	@go run ./cmd/bspagent/*.go \
	--redis-url="redis://username:@localhost:6379/0?topic=replication#replicate" \
	--avro-codec-path="./codec/block-elrond.avsc" \
	--binary-file-path="./bin/block-elrond/" \
	--gcp-svc-account="/Users/pranay/.config/gcloud/bsp-2.json" \
	--replica-bucket="covalenthq-geth-block-specimen" \
	--segment-length=1 \
	--proof-chain-address=0xbFCa723A2661350f86f397CEdF807D6e596d7874 \
	--consumer-timeout=80 \
	--websocket-urls="34.66.210.112:20000 34.66.210.112:20001 34.66.210.112:20002 34.66.210.112:20003" 

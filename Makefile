.PHONY: 

all: ign-staticnet

ign-staticnet:
	go build

clean:
	rm ign-staticnet


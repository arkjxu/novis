CXX = go
CXXENV=

TESTSRC = tests/*.go
EXAMPLESRC = examples/*.go

all: tar

test:
	$(CXXENV) $(CXX) test -count=1 $(TESTSRC)

example:
	$(CXXENV) $(CXX) run $(EXAMPLESRC)
	
tar:
	tar --exclude='*.jwk' --exclude='*.db' --exclude='.git' --exclude='.DS_Store' -czvf novis.tar.gz ./*

clean:
	$(CXX) clean -testcache
	rm -rf novis.tar.gz
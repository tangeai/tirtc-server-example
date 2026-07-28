CC      := gcc
CFLAGS  := -Wall -Wextra -std=c11 -O2
LDFLAGS := -lcrypto

TARGET  := test_tirtc_signing
OBJS    := tirtc_signing.o test_tirtc_signing.o

.PHONY: all clean test

all: $(TARGET)

$(TARGET): $(OBJS)
	$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

tirtc_signing.o: tirtc_signing.c tirtc_signing.h
	$(CC) $(CFLAGS) -c -o $@ tirtc_signing.c

test_tirtc_signing.o: test_tirtc_signing.c tirtc_signing.h
	$(CC) $(CFLAGS) -c -o $@ test_tirtc_signing.c

test: $(TARGET)
	./$(TARGET)

clean:
	rm -f $(TARGET) $(OBJS)

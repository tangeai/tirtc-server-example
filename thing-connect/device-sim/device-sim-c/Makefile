# Makefile — C device simulator (embedded-reference implementation)
# Target: Ubuntu 22.04 x86_64; portable to embedded Linux/RTOS

TARGET  := device-sim
SRCDIR  := src
OBJDIR  := obj
WERROR  ?= 0
SANITIZE ?=

# The repository keeps SDK releases under <platform>/<version>.  Allow the
# target platform/version to be selected for native or cross compilation.
SDK_PLATFORM ?= linux-x86_64
SDK_VERSION  ?= 2.2.1
SDK_DIR      ?= ../sdk/$(SDK_PLATFORM)/$(SDK_VERSION)
MBEDTLS_SDK_VERSION ?= 0.1.6
MBEDTLS_SDK_DIR ?= ../sdk/$(SDK_PLATFORM)/$(MBEDTLS_SDK_VERSION)
SDK_INC := -I$(SDK_DIR)/include
SDK_LIB := -L$(SDK_DIR)/lib -lTiRTC

PKG_CFLAGS  := $(shell pkg-config --cflags libcurl 2>/dev/null)
PKG_LDFLAGS := $(shell pkg-config --libs   libcurl 2>/dev/null)

# cjson doesn't ship a .pc file on Ubuntu — add manually
PKG_CFLAGS  += -I/usr/include/cjson
PKG_LDFLAGS += -lcjson

CFLAGS  := -Wall -Wextra -std=gnu99 -g -O2 -pthread -D_GNU_SOURCE
CFLAGS  += $(SDK_INC) -I$(MBEDTLS_SDK_DIR)/include $(PKG_CFLAGS)
ifeq ($(WERROR),1)
CFLAGS  += -Werror
endif
ifneq ($(SANITIZE),)
CFLAGS  += -fsanitize=$(SANITIZE) -fno-omit-frame-pointer
endif

LDFLAGS := -pthread
LDFLAGS += -Wl,-rpath,'$$ORIGIN/../sdk/$(SDK_PLATFORM)/$(SDK_VERSION)/lib'
LDFLAGS += $(SDK_LIB)
LDFLAGS += $(PKG_LDFLAGS)
LDFLAGS += -lmosquitto
LDFLAGS += -lm
ifneq ($(SANITIZE),)
LDFLAGS += -fsanitize=$(SANITIZE)
endif

SRCS := $(wildcard $(SRCDIR)/*.c)
OBJS := $(SRCS:$(SRCDIR)/%.c=$(OBJDIR)/%.o)
DEPS := $(OBJS:.o=.d)

.PHONY: all clean run help check-sdk test

all: check-sdk $(TARGET)

check-sdk:
	@test -f "$(SDK_DIR)/include/tirtc/tiRTC.h" && test -f "$(SDK_DIR)/lib/libTiRTC.so" || \
		( echo "Missing TiRTC C SDK: $(SDK_DIR)"; \
		  echo "Set SDK_PLATFORM and SDK_VERSION to a bundled or downloaded SDK release."; \
		  exit 1 )
	@test -f "$(MBEDTLS_SDK_DIR)/include/mbedtls/md.h" && test -f "$(MBEDTLS_SDK_DIR)/include/mbedtls/base64.h" || \
		( echo "Missing SDK-provided mbedTLS headers: $(MBEDTLS_SDK_DIR)/include/mbedtls"; \
		  echo "Set MBEDTLS_SDK_VERSION to a compatible SDK release."; \
		  exit 1 )

$(TARGET): $(OBJS)
	@echo "  LD    $@"
	@$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

$(OBJDIR)/%.o: $(SRCDIR)/%.c | $(OBJDIR)
	@echo "  CC    $<"
	@$(CC) $(CFLAGS) -MMD -MP -c $< -o $@

$(OBJDIR):
	@mkdir -p $(OBJDIR)

-include $(DEPS)

clean:
	rm -rf $(OBJDIR) $(TARGET)

TEST_TARGET := $(OBJDIR)/test-core
TEST_SRCS := tests/test_core.c $(SRCDIR)/common.c $(SRCDIR)/media_format.c \
	$(SRCDIR)/file_media_source.c $(SRCDIR)/sdk_callback_guard.c \
	$(SRCDIR)/media_subscription_policy.c \
	$(SRCDIR)/session_coordinator.c $(SRCDIR)/session_arbiter.c \
	$(SRCDIR)/http_tls.c $(SRCDIR)/media_rx_log.c $(SRCDIR)/tirtc_ai.c \
	$(SRCDIR)/tirtc_voip.c

$(TEST_TARGET): $(TEST_SRCS) | $(OBJDIR)
	@echo "  CC    $@"
	@$(CC) $(CFLAGS) -DDEVICE_SIM_TESTING -I$(SRCDIR) -o $@ $(TEST_SRCS) $(LDFLAGS)

test: check-sdk $(TEST_TARGET)
	@LD_LIBRARY_PATH="$(SDK_DIR)/lib:$${LD_LIBRARY_PATH}" $(TEST_TARGET)

run: $(TARGET)
	./$(TARGET)

help:
	@echo "Usage:"
	@echo "  make          Build $(TARGET)"
	@echo "  make WERROR=1 Build with warnings treated as errors"
	@echo "  make SANITIZE=address WERROR=1  Build with AddressSanitizer"
	@echo "  make test     Run C core tests"
	@echo "  make clean    Remove build artifacts"
	@echo ""
	@echo "Run examples:"
	@echo "  # Unified device runtime (streaming, VoIP, AI and device calls)"
	@echo "  DEVICE_ID=DEV000001 DEVICE_KEY=your-key ./$(TARGET)"
	@echo ""
	@echo "  # Unbound flow (scan-code binding)"
	@echo "  ./$(TARGET) --mac AA:BB:CC:DD:EE:FF"

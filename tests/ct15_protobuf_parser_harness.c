#include <errno.h>
#include <inttypes.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "proto/toniebox.pb.freshness-check.fc-request.pb-c.h"
#include "proto/toniebox.pb.freshness-check.fc-response.pb-c.h"

static int hex_nibble(char value)
{
    if (value >= '0' && value <= '9') {
        return value - '0';
    }
    if (value >= 'a' && value <= 'f') {
        return value - 'a' + 10;
    }
    if (value >= 'A' && value <= 'F') {
        return value - 'A' + 10;
    }
    return -1;
}

static uint8_t *decode_hex(const char *text, size_t *decoded_length)
{
    size_t text_length = strlen(text);
    size_t output_length;
    uint8_t *output;
    size_t index;

    if (text_length == 0 || text_length % 2 != 0) {
        return NULL;
    }
    output_length = text_length / 2;
    output = malloc(output_length);
    if (output == NULL) {
        return NULL;
    }
    for (index = 0; index < output_length; ++index) {
        int high = hex_nibble(text[index * 2]);
        int low = hex_nibble(text[index * 2 + 1]);
        if (high < 0 || low < 0) {
            free(output);
            return NULL;
        }
        output[index] = (uint8_t)((high << 4) | low);
    }
    *decoded_length = output_length;
    return output;
}

static int parse_u64(const char *text, uint64_t *value)
{
    char *end = NULL;
    unsigned long long parsed;

    errno = 0;
    parsed = strtoull(text, &end, 0);
    if (errno != 0 || end == text || *end != '\0') {
        return 0;
    }
    *value = (uint64_t)parsed;
    return 1;
}

static int parse_i32(const char *text, int32_t *value)
{
    char *end = NULL;
    long parsed;

    errno = 0;
    parsed = strtol(text, &end, 10);
    if (errno != 0 || end == text || *end != '\0' || parsed < INT32_MIN || parsed > INT32_MAX) {
        return 0;
    }
    *value = (int32_t)parsed;
    return 1;
}

int main(int argc, char **argv)
{
    uint8_t *request_bytes = NULL;
    uint8_t *truncated_bytes = NULL;
    uint8_t *expected_response = NULL;
    uint8_t *packed_response = NULL;
    size_t request_length = 0;
    size_t truncated_length = 0;
    size_t expected_response_length = 0;
    size_t packed_length;
    uint64_t expected_uid;
    uint64_t parsed_audio_id;
    TonieFreshnessCheckRequest *request = NULL;
    TonieFreshnessCheckRequest *truncated = NULL;
    TonieFreshnessCheckResponse response = TONIE_FRESHNESS_CHECK_RESPONSE__INIT;
    int result = 1;

    if (argc != 13) {
        fprintf(stderr, "usage: %s REQUEST_HEX TRUNCATED_HEX RESPONSE_HEX UID AUDIO_ID FIELD2 MAX_SPK SLAP_EN SLAP_DIR FIELD6 MAX_HDP LED\n", argv[0]);
        return 2;
    }

    request_bytes = decode_hex(argv[1], &request_length);
    truncated_bytes = decode_hex(argv[2], &truncated_length);
    expected_response = decode_hex(argv[3], &expected_response_length);
    if (request_bytes == NULL || truncated_bytes == NULL || expected_response == NULL) {
        fprintf(stderr, "invalid hexadecimal fixture input\n");
        goto cleanup;
    }
    if (!parse_u64(argv[4], &expected_uid) || !parse_u64(argv[5], &parsed_audio_id) || parsed_audio_id > UINT32_MAX) {
        fprintf(stderr, "invalid UID or audio ID fixture value\n");
        goto cleanup;
    }
    if (!parse_i32(argv[6], &response.field2) ||
        !parse_i32(argv[7], &response.max_vol_spk) ||
        !parse_i32(argv[8], &response.slap_en) ||
        !parse_i32(argv[9], &response.slap_dir) ||
        !parse_i32(argv[10], &response.field6) ||
        !parse_i32(argv[11], &response.max_vol_hdp) ||
        !parse_i32(argv[12], &response.led)) {
        fprintf(stderr, "invalid response fixture value\n");
        goto cleanup;
    }

    request = tonie_freshness_check_request__unpack(NULL, request_length, request_bytes);
    if (request == NULL) {
        fprintf(stderr, "positive request was rejected by protobuf-c\n");
        goto cleanup;
    }
    if (request->n_tonie_infos != 1 || request->tonie_infos == NULL || request->tonie_infos[0] == NULL) {
        fprintf(stderr, "positive request did not contain exactly one tonie_infos entry\n");
        goto cleanup;
    }
    if (request->tonie_infos[0]->uid != expected_uid ||
        request->tonie_infos[0]->audio_id != (uint32_t)parsed_audio_id) {
        fprintf(stderr, "decoded UID/audio ID did not match the fixture\n");
        goto cleanup;
    }

    truncated = tonie_freshness_check_request__unpack(NULL, truncated_length, truncated_bytes);
    if (truncated != NULL) {
        fprintf(stderr, "truncated request was accepted by protobuf-c\n");
        goto cleanup;
    }

    packed_length = tonie_freshness_check_response__get_packed_size(&response);
    if (packed_length != expected_response_length) {
        fprintf(stderr, "packed response length %zu did not match fixture length %zu\n", packed_length, expected_response_length);
        goto cleanup;
    }
    packed_response = malloc(packed_length);
    if (packed_response == NULL) {
        fprintf(stderr, "unable to allocate packed response\n");
        goto cleanup;
    }
    if (tonie_freshness_check_response__pack(&response, packed_response) != packed_length ||
        memcmp(packed_response, expected_response, packed_length) != 0) {
        fprintf(stderr, "packed response bytes did not match the fixture\n");
        goto cleanup;
    }

    printf("accepted positive request; rejected %zu-byte truncation; packed %zu-byte response\n",
           truncated_length, packed_length);
    result = 0;

cleanup:
    tonie_freshness_check_request__free_unpacked(request, NULL);
    tonie_freshness_check_request__free_unpacked(truncated, NULL);
    free(request_bytes);
    free(truncated_bytes);
    free(expected_response);
    free(packed_response);
    return result;
}

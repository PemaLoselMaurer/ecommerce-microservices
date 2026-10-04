cd ~/lab2/example
./scripts/e2e.sh > /tmp/e2e-all.log 2>&1
grep -E "✘|  [0-9]+ (passed|failed)|^\s+\[chromium\]|All end-to-end|Some end-to-end" /tmp/e2e-all.log | head -20
# leave the demo running with a normal configuration
PAYMENT_DELAY=0s INVENTORY_FLAKY=false ./scripts/start-services.sh > /dev/null 2>&1
echo "services restarted"

echo "=== bucket sizes ==="
sudo -n docker exec deploy-minio-1 sh -c 'du -sh /data/blockchain-* 2>/dev/null'
echo "=== object counts ==="
sudo -n docker exec deploy-minio-1 sh -c 'for d in /data/blockchain-*; do printf "%s " "$d"; find "$d" -type d -name "xl.meta" | wc -l; done' 2>/dev/null
echo "=== pull mc ==="
sudo -n docker pull minio/mc:latest 2>&1 | tail -2
echo "=== mc alias + du ==="
sudo -n docker run --rm --network host -e MC_HOST_local="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9002" minio/mc:latest du local 2>&1 | tail -12
echo SCRIPT_END

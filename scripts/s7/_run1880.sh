grep -E '^MINIO_' /home/euripides/projects/blockchain-platform/deploy/.env
sudo -n docker ps --format '{{.Names}} {{.Ports}}' | grep minio
echo SCRIPT_END

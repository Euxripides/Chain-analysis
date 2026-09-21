# 二进制目录清理记录（2026-09-16，为备份释放磁盘空间）
清理前 df:
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   96G   44G  69% /

## bin.new（旧部署脚本留下的暂存目录，systemd 单元不引用）
total 3133944
drwxrwxr-x  2 euripides euripides     4096 Sep 16 06:25 .
drwxrwxr-x 12 euripides euripides     4096 Sep 16 04:38 ..
-rwxrwxr-x  1 euripides euripides 56893872 Sep 16 03:52 api-activitywindow-calib
-rwxrwxr-x  1 euripides euripides 56776926 Sep 15 12:09 api-anchorfix
-rwxrwxr-x  1 euripides euripides 56781991 Sep 15 12:09 api-anchorfix-calib
-rwxrwxr-x  1 euripides euripides 56743925 Sep 14 22:11 api-auth007-deny
-rwxrwxr-x  1 euripides euripides 56748438 Sep 14 22:32 api-auth007-deny-calib
-rwxrwxr-x  1 euripides euripides 56642329 Sep 14 12:57 api-auth007-s3
-rwxrwxr-x  1 euripides euripides 56503026 Sep 14 04:46 api-auth007-seam
-rwxrwxr-x  1 euripides euripides 56507387 Sep 14 04:46 api-calib-auth007
-rwxrwxr-x  1 euripides euripides 56647890 Sep 14 12:57 api-calib-auth007-s3
-rwxrwxr-x  1 euripides euripides 56623926 Sep 14 02:29 api-calib-p1a
-rwxrwxr-x  1 euripides euripides 56636567 Sep 14 03:12 api-calib-p1a-adminrevoke
-rwxrwxr-x  1 euripides euripides 56892252 Sep 15 23:58 api-dataops-calib
-rwxrwxr-x  1 euripides euripides 56774109 Sep 15 08:44 api-expcap
-rwxrwxr-x  1 euripides euripides 56774910 Sep 15 08:44 api-expcap-calib
-rwxrwxr-x  1 euripides euripides 56774910 Sep 15 08:44 api-expcap-calib-r1-75b53863
-rwxrwxr-x  1 euripides euripides 56774109 Sep 15 08:44 api-expcap-r1-3bd72b5d
-rwxrwxr-x  1 euripides euripides 56775568 Sep 15 08:51 api-expcap-r2

## 保留的部署备份（回滚材料）
bin/deploy-backups-20260916T084450Z
bin/deploy-backups-20260916T075720Z
bin/deploy-backups-20260916T070752Z
bin/deploy-backups-20260916T070553Z
bin/deploy-backups-20260916T063716Z
bin/deploy-backups-20260916T061208Z
bin/deploy-backups-20260916T060921Z
bin/deploy-backups-20260916T060651Z
bin/deploy-backups-20260916T060358Z
3.0G	bin.new

清理后 df:
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   92G   48G  66% /

## 历史二进制副本清理（同一轮，第二批）
删除前 df:
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   92G   48G  67% /
删除对象：bin 下所有 历史副本/校准构建（匹配 api.prev-* / api.bak-* / *.b2 / *.new* / api.deployed-* / *-calib）
保留：api / worker / migrate / loadprobe / auditverify / 各 fixture 与 probe 工具 + 最近 4 个 deploy-backups-*
删除清单（sha256 不逐一记录，文件为可重建产物）：
bin/api.bak-address-controls-20260909
bin/api.bak-dependency-upgrade-20260913T015815Z
bin/api.bak-dualsystem-20260912-025132
bin/api.bak-dualsystem-20260912-025326
bin/api.bak-dualsystem-20260912-025607
bin/api.bak-dualsystem-20260912-025739
bin/api.bak-dualsystem-20260912-025857
bin/api.bak-dualsystem-20260912-031742
bin/api.bak-dualsystem-20260912-032056
bin/api.bak-dualsystem-20260912-032610
bin/api.bak-dualsystem-20260912-032939
bin/api.bak-dualsystem-20260912-135729
bin/api.bak-dualsystem-20260912-135903
bin/api.bak-dualsystem-20260912-140358
bin/api.bak-dualsystem-20260912-140551
bin/api.bak-dualsystem-20260912-140747
bin/api.bak-dualsystem-20260912-141054
bin/api.bak-dualsystem-20260912-144633
bin/api.bak-dualsystem-20260912-144844
bin/api.bak-dualsystem-20260912-145020
bin/api.bak-dualsystem-20260912-145224
bin/api.bak-dualsystem-20260912-145517
bin/api.bak-dualsystem-20260912-145920
bin/api.bak-dualsystem-20260912-150100
bin/api.bak-dualsystem-20260912-150157
bin/api.bak-dualsystem-20260912-150509
bin/api.bak-dualsystem-20260912-151825
bin/api.bak-dualsystem-20260912-161702
bin/api.bak-dualsystem-20260912-170818
bin/api.bak-dualsystem-20260912-180837
bin/api.bak-dualsystem-20260912-183341
bin/api.bak-dualsystem-20260912-184512
bin/api.bak-dualsystem-20260912-185736
bin/api.bak-dualsystem-20260912-191149
bin/api.bak-dualsystem-20260912-191916
bin/api.bak-dualsystem-20260912-192125
bin/api.bak-dualsystem-20260912-192324
bin/api.bak-dualsystem-20260912-194829
bin/api.bak-dualsystem-20260912-195242
bin/api.bak-dualsystem-20260912-195834
bin/api.bak-dualsystem-20260912-200005
bin/api.bak-dualsystem-20260912-200156
bin/api.bak-dualsystem-20260912-202559
bin/api.bak-dualsystem-20260912-205539
bin/api.bak-dualsystem-20260912-211003
bin/api.bak-dualsystem-20260912-212457
bin/api.bak-dualsystem-20260912-212611
bin/api.bak-dualsystem-20260912-215136
bin/api.bak-dualsystem-20260912-224725
bin/api.bak-dualsystem-20260912-233320
bin/api.bak-dualsystem-20260912-233531
bin/api.bak-dualsystem-20260912-235409
bin/api.bak-p0-20260912T2050Z
bin/api.bak-p1a-20260913T193533Z
bin/api.bak-p1a-20260913T193605Z
bin/api.bak-p1a-20260913T193621Z
bin/api.bak-p1a-20260913T201754Z
bin/api.bak-p1a-20260913T211704Z
bin/api.bak-partial-export-20260909
bin/api.bak-pausefix-20260915T092250Z
bin/api.bak-pausefix2-20260915T092527Z
bin/api.bak-pausefix3-20260915T092827Z
bin/api.bak-r10-20260913T025912Z
bin/api.bak-r10-20260913T160151Z
bin/api.bak-r10v2-20260913T030543Z
bin/api.bak-r10v2-20260913T031948Z
bin/api.bak-r10v2-20260913T055509Z
bin/api.bak-r10v2-20260913T060152Z
bin/api.bak-r2-20260912T2105Z
bin/api.bak-r9-20260913T022127Z
bin/api.bak-s4-dev
bin/api.bak-s6-20260912-211609
bin/api.bak-s7-20260912T162927Z
bin/api.bak-s7v4-20260913T163304Z
bin/api.bak-s7v4-20260913T172409Z
bin/api.bak-s7v4-20260913T173019Z
bin/api.bak-s7v4-20260913T184801Z
bin/api.bak-s7v4-20260913T185155Z
bin/api.bak-sem-20260912T2130Z
bin/api.bak-volguard-20260915T093948Z
bin/api.bak-volguard-20260915T094119Z
bin/api.deployed-910b7ca2-20260915T083048Z
bin/api.new.legacyconsole
bin/api.prev-20260915T213013Z
bin/api.prev-20260915T213602Z
bin/api.prev-20260915T231233Z
bin/api.prev-20260915T232305Z
bin/api.prev-20260915T232902Z
bin/api.prev-20260916T032326Z
bin/migrate.bak-dependency-upgrade-20260913T015815Z
bin/migrate.bak-p0-20260912T2050Z
bin/migrate.bak-r9-20260913T022127Z
bin/worker.bak-dependency-upgrade-20260913T015815Z
bin/worker.bak-dualsystem-20260912-025132
bin/worker.bak-dualsystem-20260912-025326
bin/worker.bak-dualsystem-20260912-025607
bin/worker.bak-dualsystem-20260912-025739
bin/worker.bak-dualsystem-20260912-025857
bin/worker.bak-dualsystem-20260912-031742
bin/worker.bak-dualsystem-20260912-032056
bin/worker.bak-dualsystem-20260912-032610
bin/worker.bak-dualsystem-20260912-032939
bin/worker.bak-dualsystem-20260912-135729
bin/worker.bak-dualsystem-20260912-135903
bin/worker.bak-dualsystem-20260912-140358
bin/worker.bak-dualsystem-20260912-140551
bin/worker.bak-dualsystem-20260912-140747
bin/worker.bak-dualsystem-20260912-141054
bin/worker.bak-dualsystem-20260912-144633
bin/worker.bak-dualsystem-20260912-144844
bin/worker.bak-dualsystem-20260912-145020
bin/worker.bak-dualsystem-20260912-145224
bin/worker.bak-dualsystem-20260912-145517
bin/worker.bak-dualsystem-20260912-145920
bin/worker.bak-dualsystem-20260912-150100
bin/worker.bak-dualsystem-20260912-150157
bin/worker.bak-dualsystem-20260912-150509
bin/worker.bak-dualsystem-20260912-151825
bin/worker.bak-dualsystem-20260912-161702
bin/worker.bak-dualsystem-20260912-170818
bin/worker.bak-dualsystem-20260912-180837
bin/worker.bak-dualsystem-20260912-183341
bin/worker.bak-dualsystem-20260912-184512
bin/worker.bak-dualsystem-20260912-185736
bin/worker.bak-dualsystem-20260912-191149
bin/worker.bak-dualsystem-20260912-191916
bin/worker.bak-dualsystem-20260912-192125
bin/worker.bak-dualsystem-20260912-192324
bin/worker.bak-dualsystem-20260912-194829
bin/worker.bak-dualsystem-20260912-195242
bin/worker.bak-dualsystem-20260912-195834
bin/worker.bak-dualsystem-20260912-200005
bin/worker.bak-dualsystem-20260912-200156
bin/worker.bak-dualsystem-20260912-202559
bin/worker.bak-dualsystem-20260912-205539
bin/worker.bak-dualsystem-20260912-211003
bin/worker.bak-dualsystem-20260912-212457
bin/worker.bak-dualsystem-20260912-212611
bin/worker.bak-dualsystem-20260912-215136
bin/worker.bak-dualsystem-20260912-224725
bin/worker.bak-dualsystem-20260912-233320
bin/worker.bak-dualsystem-20260912-233531
bin/worker.bak-dualsystem-20260912-235409
bin/worker.bak-p0-20260912T2050Z
bin/worker.bak-pausefix-20260915T092250Z
bin/worker.bak-pausefix2-20260915T092527Z
bin/worker.bak-r2-20260912T2105Z
bin/worker.bak-r9-20260913T022127Z
bin/worker.bak-s6-20260912-211609
bin/worker.bak-sem-20260912T2130Z
bin/worker.bak-v14-20260912T2125Z
bin/worker.bak-volguard-20260915T093948Z
bin/worker.bak-volguard-20260915T094119Z

删除后 df:
/dev/mapper/ubuntu--vg-ubuntu--lv  147G   85G   55G  61% /

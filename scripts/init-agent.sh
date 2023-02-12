#!/bin/bash

TMP="/tmp/octopus-wechat"
DIR="/home/user/octopus-wechat"

if [ ! -d "$DIR" ]; then
    echo "Create agent folder"
    mkdir -p $DIR
fi

mkdir -p $TMP
cd $TMP

if [ ! -f "$DIR/octopus-wechat-x86.exe" ]; then
    echo "Download agent"
    URL=$(curl -s https://api.github.com/repos/duo/octopus-wechat/releases/latest | grep "browser_download_url.*x86.exe" | cut -d : -f 2,3 | tr -d \")
    wget -q $URL
    cp *.exe $DIR
fi

if [[ ! -f "$DIR/wxDriver.dll" || ! -f "$DIR/SWeChatRobot.dll" ]]; then
    echo "Download dll"
    URL=$(curl -s https://api.github.com/repos/ljc545w/ComWeChatRobot/releases/latest | grep "browser_download_url.*zip" | cut -d : -f 2,3 | tr -d \")
    wget -q $URL
    unzip -qq *.zip
    cp http/*.dll $DIR
fi

rm -rf $TMP

//go:build ignore

// Generate the Wails edition badge with Go's standard image library.
package main

import (
 "image"
 "image/color"
 "image/draw"
 "image/png"
 "math"
 "os"
 "path/filepath"
)

func main(){root,e:=os.Getwd();must(e);f,e:=os.Open(filepath.Join(root,"electron/assets/icon.png"));must(e);src,e:=png.Decode(f);f.Close();must(e);im:=image.NewRGBA(src.Bounds());draw.Draw(im,im.Bounds(),src,src.Bounds().Min,draw.Src);size:=float64(im.Bounds().Dx());x,y,edge:=size*.66,size*.66,size*.29;radius:=size*.07;stroke:=math.Max(2,size*.012)
 inside:=func(px,py,inset float64)bool{l,t,r,b:=x+inset,y+inset,x+edge-inset,y+edge-inset;rr:=math.Max(0,radius-inset);cx:=math.Max(l+rr,math.Min(r-rr,px));cy:=math.Max(t+rr,math.Min(b-rr,py));return px>=l&&px<=r&&py>=t&&py<=b&&(px-cx)*(px-cx)+(py-cy)*(py-cy)<=rr*rr}
 for yy:=int(y);yy<=int(y+edge);yy++{for xx:=int(x);xx<=int(x+edge);xx++{if inside(float64(xx),float64(yy),0){c:=color.RGBA{251,250,246,255};if inside(float64(xx),float64(yy),stroke){c=color.RGBA{37,99,235,255}};im.SetRGBA(xx,yy,c)}}}
 pts:=[][2]float64{{.18,.27},{.32,.74},{.50,.40},{.68,.74},{.82,.27}};brush:=math.Max(2,edge*.09)/2
 for i:=1;i<len(pts);i++{ax,ay:=x+edge*pts[i-1][0],y+edge*pts[i-1][1];bx,by:=x+edge*pts[i][0],y+edge*pts[i][1];steps:=int(math.Ceil(math.Hypot(bx-ax,by-ay)*2));for j:=0;j<=steps;j++{u:=float64(j)/float64(steps);cx,cy:=ax+(bx-ax)*u,ay+(by-ay)*u;for yy:=int(cy-brush);yy<=int(cy+brush);yy++{for xx:=int(cx-brush);xx<=int(cx+brush);xx++{if math.Hypot(float64(xx)-cx,float64(yy)-cy)<=brush{im.SetRGBA(xx,yy,color.RGBA{255,255,255,255})}}}}}
 out:=filepath.Join(root,"desktop-wails/build/appicon.png");must(os.MkdirAll(filepath.Dir(out),0755));f,e=os.Create(out);must(e);must(png.Encode(f,im));must(f.Close())
}
func must(e error){if e!=nil{panic(e)}}
